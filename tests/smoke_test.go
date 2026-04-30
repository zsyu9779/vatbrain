package tests

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	neodriver "github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/db/neo4j"
	"github.com/vatbrain/vatbrain/internal/db/pgvector"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store/neo4jpg"
)

func TestSmoke_Neo4j_WriteRead(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	neo4jClient, err := neo4j.NewClient(ctx, neo4j.Config{
		URI:                  "bolt://localhost:7687",
		Username:             "neo4j",
		Password:             "vatbrain",
		Database:             "neo4j",
		MaxConnectionPoolSize: 10,
	})
	require.NoError(t, err, "neo4j must be available")
	defer neo4jClient.Close(ctx)

	memoryID := uuid.New().String()

	_, err = neo4jClient.ExecuteWrite(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
		_, runErr := tx.Run(ctx, `
			CREATE (e:EpisodicMemory {
				id: $id, project_id: 'smoketest', language: 'go',
				summary: 'smoke test memory', weight: 1.0, created_at: datetime()
			})
		`, map[string]any{"id": memoryID})
		return nil, runErr
	})
	require.NoError(t, err)

	raw, err := neo4jClient.ExecuteRead(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
		records, runErr := tx.Run(ctx, `
			MATCH (e:EpisodicMemory {id: $id}) RETURN e.summary AS summary, e.weight AS weight
		`, map[string]any{"id": memoryID})
		if runErr != nil {
			return nil, runErr
		}
		if !records.Next(ctx) {
			return nil, records.Err()
		}
		r := records.Record()
		summary, _, _ := neodriver.GetRecordValue[string](r, "summary")
		weight, _, _ := neodriver.GetRecordValue[float64](r, "weight")
		return []any{summary, weight}, nil
	})
	require.NoError(t, err)

	values := raw.([]any)
	assert.Equal(t, "smoke test memory", values[0])
	assert.Equal(t, 1.0, values[1])

	// Cleanup
	_, _ = neo4jClient.ExecuteWrite(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
		_, runErr := tx.Run(ctx,
			`MATCH (e:EpisodicMemory {id: $id}) DELETE e`, map[string]any{"id": memoryID})
		return nil, runErr
	})
}

func TestSmoke_Pgvector_WriteSearch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pgClient, err := pgvector.NewClient(ctx, pgvector.Config{
		Host:     "localhost",
		Port:     5432,
		User:     "vatbrain",
		Password: "vatbrain",
		Database: "vatbrain",
		MaxConns: 5,
	})
	require.NoError(t, err, "pgvector must be available")
	defer pgClient.Close()

	memoryID := uuid.New().String()
	embedding := make([]float32, 1536)
	embedding[0] = 0.1
	embedding[1] = 0.2

	err = pgClient.InsertEmbedding(ctx, memoryID, embedding, "smoke test summary",
		"smoketest", "go", "test", map[string]any{"key": "value"})
	require.NoError(t, err)

	queryVec := make([]float32, 1536)
	queryVec[0] = 0.1
	queryVec[1] = 0.2

	results, err := pgClient.SimilaritySearch(ctx, queryVec, 5, []string{memoryID})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, memoryID, results[0].MemoryID)
	assert.Equal(t, "smoke test summary", results[0].SummaryText)

	// Cleanup
	err = pgClient.DeleteByMemoryID(ctx, memoryID)
	require.NoError(t, err)
}

func TestSmoke_LinkOnWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	neo4jClient, err := neo4j.NewClient(ctx, neo4j.Config{
		URI:      "bolt://localhost:7687", Username: "neo4j", Password: "vatbrain",
		Database: "neo4j", MaxConnectionPoolSize: 10,
	})
	require.NoError(t, err)
	defer neo4jClient.Close(ctx)

	pgClient, err := pgvector.NewClient(ctx, pgvector.Config{
		Host: "localhost", Port: 5432, User: "vatbrain", Password: "vatbrain",
		Database: "vatbrain", MaxConns: 5,
	})
	require.NoError(t, err)
	defer pgClient.Close()

	store, err := neo4jpg.NewStore(ctx, neo4jClient, pgClient)
	require.NoError(t, err)

	projectID := "smoketest_link"
	now := time.Now().UTC()

	// Create two related memories via Store.
	mem1ID := uuid.New()
	mem2ID := uuid.New()
	summaries := []string{
		"redis connection pool exhausted at maxconns=50 causing timeout",
		"redis pool timeout when connecting to primary node",
	}
	ids := []uuid.UUID{mem1ID, mem2ID}

	for i, mID := range ids {
		err = store.WriteEpisodic(ctx, &models.EpisodicMemory{
			ID:        mID,
			ProjectID: projectID,
			Language:  "go",
			Summary:   summaries[i],
			Weight:    1.0,
			CreatedAt: now,
		})
		require.NoError(t, err)
	}

	// Trigger LinkOnWrite.
	core.LinkOnWrite(ctx, store, mem1ID, summaries[0], projectID)

	// Verify RELATES_TO edge was created.
	edges, err := store.GetEdges(ctx, mem1ID, "RELATES_TO", "out")
	require.NoError(t, err)
	assert.Len(t, edges, 1, "LinkOnWrite should create 1 RELATES_TO edge")
	assert.Equal(t, mem2ID, edges[0].ToID)

	// Cleanup
	for _, mID := range ids {
		_, _ = neo4jClient.ExecuteWrite(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
			_, runErr := tx.Run(ctx,
				`MATCH (e:EpisodicMemory {id: $id}) DETACH DELETE e`, map[string]any{"id": mID.String()})
			return nil, runErr
		})
	}
}
