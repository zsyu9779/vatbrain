package core

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
)

func makeScanResult(projectID, taskType, summary string) store.EpisodicScanItem {
	return store.EpisodicScanItem{
		ID:        uuid.New(),
		ProjectID: projectID,
		TaskType:  models.TaskType(taskType),
		Summary:   summary,
	}
}

func TestClusterByPattern_GroupsByProjectAndTaskType(t *testing.T) {
	eps := []store.EpisodicScanItem{
		makeScanResult("projA", "debug", "debug session 1"),
		makeScanResult("projA", "debug", "debug session 2"),
		makeScanResult("projA", "debug", "debug session 3"),
		makeScanResult("projA", "feature", "feature work 1"),
		makeScanResult("projB", "debug", "projB debug"),
	}

	clusters := clusterByPattern(eps, 2)

	assert.Len(t, clusters, 1)
	assert.Equal(t, "projA", clusters[0].ProjectID)
	assert.Equal(t, models.TaskType("debug"), clusters[0].TaskType)
	assert.Len(t, clusters[0].Episodics, 3)
}

func TestClusterByPattern_EmptyInput(t *testing.T) {
	clusters := clusterByPattern(nil, 3)
	assert.Empty(t, clusters)
}

func TestClusterByPattern_BelowMinSize(t *testing.T) {
	eps := []store.EpisodicScanItem{
		makeScanResult("projA", "debug", "s1"),
		makeScanResult("projA", "debug", "s2"),
	}

	clusters := clusterByPattern(eps, 3)
	assert.Empty(t, clusters)
}

func TestClusterByPattern_AtMinSize(t *testing.T) {
	eps := []store.EpisodicScanItem{
		makeScanResult("projA", "debug", "s1"),
		makeScanResult("projA", "debug", "s2"),
		makeScanResult("projA", "debug", "s3"),
	}

	clusters := clusterByPattern(eps, 3)
	assert.Len(t, clusters, 1)
	assert.Len(t, clusters[0].Episodics, 3)
}

func TestClusterByPattern_MultipleClusters(t *testing.T) {
	eps := []store.EpisodicScanItem{
		makeScanResult("projA", "debug", "a1"), makeScanResult("projA", "debug", "a2"),
		makeScanResult("projA", "debug", "a3"),
		makeScanResult("projB", "feature", "b1"), makeScanResult("projB", "feature", "b2"),
		makeScanResult("projB", "feature", "b3"),
	}

	clusters := clusterByPattern(eps, 3)
	assert.Len(t, clusters, 2)
}

func TestExtractRule_ProducesContent(t *testing.T) {
	e := &ConsolidationEngine{}
	cl := PatternCluster{
		ProjectID: "test-proj",
		TaskType:  models.TaskTypeDebug,
		Episodics: []store.EpisodicScanItem{
			makeScanResult("test-proj", "debug", "nil pointer in handler"),
			makeScanResult("test-proj", "debug", "nil pointer in handler again"),
		},
	}

	rule := e.extractRule(t.Context(), cl)
	assert.Contains(t, rule, "test-proj/debug")
	assert.Contains(t, rule, "nil pointer in handler")
	assert.True(t, strings.Count(rule, "\n") >= 1)
}

func TestExtractRule_SingleEpisodic(t *testing.T) {
	e := &ConsolidationEngine{}
	cl := PatternCluster{
		ProjectID: "solo",
		TaskType:  models.TaskTypeRefactor,
		Episodics: []store.EpisodicScanItem{
			makeScanResult("solo", "refactor", "one event"),
		},
	}

	rule := e.extractRule(t.Context(), cl)
	assert.Contains(t, rule, "one event")
}

func TestBacktest_SufficientSize(t *testing.T) {
	e := &ConsolidationEngine{MinClusterSize: 3}
	cl := PatternCluster{
		Episodics: make([]store.EpisodicScanItem, 5),
	}
	assert.Equal(t, 1.0, e.backtest(t.Context(), cl))
}

func TestBacktest_InsufficientSize(t *testing.T) {
	e := &ConsolidationEngine{MinClusterSize: 3}
	cl := PatternCluster{
		Episodics: make([]store.EpisodicScanItem, 2),
	}
	assert.Equal(t, 0.0, e.backtest(t.Context(), cl))
}

func TestBacktest_ExactlyMinSize(t *testing.T) {
	e := &ConsolidationEngine{MinClusterSize: 3}
	cl := PatternCluster{
		Episodics: make([]store.EpisodicScanItem, 3),
	}
	assert.Equal(t, 1.0, e.backtest(t.Context(), cl))
}

func TestDefaultConsolidationEngine(t *testing.T) {
	e := DefaultConsolidationEngine()
	assert.Equal(t, 24.0, e.HoursToScan)
	assert.Equal(t, 3, e.MinClusterSize)
	assert.Equal(t, 0.7, e.AccuracyThreshold)
}
