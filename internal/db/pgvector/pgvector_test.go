package pgvector

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustConnect(t *testing.T) *Client {
	t.Helper()
	ctx := context.Background()
	c, err := NewClient(ctx, Config{
		Host:     "localhost",
		Port:     5432,
		User:     "vatbrain",
		Password: "vatbrain",
		Database: "vatbrain",
	})
	if err != nil {
		t.Skipf("skipping integration test: pgvector not available: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestPgvector_NewClient_And_HealthCheck(t *testing.T) {
	c := mustConnect(t)
	ctx := context.Background()
	assert.NoError(t, c.HealthCheck(ctx))
}

func TestPgvector_InsertGetDeleteEmbedding(t *testing.T) {
	c := mustConnect(t)
	ctx := context.Background()

	// First ensure the table exists
	_, err := c.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS episodic_embeddings (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			memory_id TEXT NOT NULL,
			embedding vector(1536),
			summary_text TEXT,
			project_id TEXT,
			language TEXT,
			task_type TEXT,
			metadata JSONB DEFAULT '{}'
		)
	`)
	require.NoError(t, err)

	memID := "550e8400-e29b-41d4-a716-446655440003"
	emb := make([]float32, 1536)
	emb[0] = 0.5
	emb[1] = 0.3

	err = c.InsertEmbedding(ctx, memID, emb, "test summary", "test-proj", "go", "debug", nil)
	require.NoError(t, err)

	// Get it back
	vec, err := c.GetEmbedding(ctx, memID)
	require.NoError(t, err)
	assert.Len(t, vec, 1536)
	assert.InDelta(t, float32(0.5), vec[0], 1e-6)

	// Delete it
	err = c.DeleteByMemoryID(ctx, memID)
	require.NoError(t, err)

	_, err = c.GetEmbedding(ctx, memID)
	assert.Error(t, err)
}

func TestPgvector_SimilaritySearch(t *testing.T) {
	c := mustConnect(t)
	ctx := context.Background()

	_, err := c.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS episodic_embeddings (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			memory_id TEXT NOT NULL,
			embedding vector(1536),
			summary_text TEXT,
			project_id TEXT,
			language TEXT,
			task_type TEXT,
			metadata JSONB DEFAULT '{}'
		)
	`)
	require.NoError(t, err)

	// Clean test data
	c.pool.Exec(ctx, `DELETE FROM episodic_embeddings WHERE project_id = 'vec-test-proj'`)

	emb1 := make([]float32, 1536)
	emb1[0] = 1.0
	emb2 := make([]float32, 1536)
	emb2[0] = 0.5
	emb2[1] = 1.0

	err = c.InsertEmbedding(ctx, "550e8400-e29b-41d4-a716-446655440001", emb1, "summary 1", "vec-test-proj", "", "", nil)
	require.NoError(t, err)
	err = c.InsertEmbedding(ctx, "550e8400-e29b-41d4-a716-446655440002", emb2, "summary 2", "vec-test-proj", "", "", nil)
	require.NoError(t, err)

	// Search with a query similar to emb1
	query := make([]float32, 1536)
	query[0] = 0.9
	// Filter to only our test IDs
	filterIDs := []string{
		"550e8400-e29b-41d4-a716-446655440001",
		"550e8400-e29b-41d4-a716-446655440002",
	}
	results, err := c.SimilaritySearch(ctx, query, 5, filterIDs)
	require.NoError(t, err)
	assert.Len(t, results, 2)
	// First result should be closest to query
	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440001", results[0].MemoryID)

	// Cleanup
	c.DeleteByMemoryID(ctx, "550e8400-e29b-41d4-a716-446655440001")
	c.DeleteByMemoryID(ctx, "550e8400-e29b-41d4-a716-446655440002")
}

func TestPgvector_DefaultConfig(t *testing.T) {
	c, err := NewClient(context.Background(), Config{
		Host:     "localhost",
		User:     "vatbrain",
		Password: "vatbrain",
		Database: "vatbrain",
	})
	if err != nil {
		t.Skipf("skipping: %v", err)
	}
	defer c.Close()
	// Port should default to 5432, MaxConns to 20
	assert.NotNil(t, c.pool)
}
