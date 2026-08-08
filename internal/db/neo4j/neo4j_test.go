package neo4j

import (
	"context"
	"testing"

	neodriver "github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustConnect(t *testing.T) *Client {
	t.Helper()
	ctx := context.Background()
	c, err := NewClient(ctx, Config{
		URI:      "bolt://localhost:7687",
		Username: "neo4j",
		Password: "vatbrain",
		Database: "neo4j",
	})
	if err != nil {
		t.Skipf("skipping integration test: neo4j not available: %v", err)
	}
	t.Cleanup(func() { c.Close(ctx) })
	return c
}

func TestNeo4j_NewClient_And_HealthCheck(t *testing.T) {
	c := mustConnect(t)
	ctx := context.Background()
	assert.NoError(t, c.HealthCheck(ctx))
}

func TestNeo4j_Session(t *testing.T) {
	c := mustConnect(t)
	ctx := context.Background()
	sess := c.Session(ctx)
	assert.NotNil(t, sess)
	sess.Close(ctx)
}

func TestNeo4j_ExecuteRead(t *testing.T) {
	c := mustConnect(t)
	ctx := context.Background()

	result, err := c.ExecuteRead(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
		return "read_ok", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "read_ok", result)
}

func TestNeo4j_ExecuteWrite(t *testing.T) {
	c := mustConnect(t)
	ctx := context.Background()

	result, err := c.ExecuteWrite(ctx, func(tx neodriver.ManagedTransaction) (any, error) {
		return "write_ok", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "write_ok", result)
}

func TestNeo4j_ConfigDefaults(t *testing.T) {
	c, err := NewClient(context.Background(), Config{
		URI:      "bolt://localhost:7687",
		Username: "neo4j",
		Password: "vatbrain",
	})
	if err != nil {
		t.Skipf("skipping: %v", err)
	}
	defer c.Close(context.Background())
	assert.Equal(t, "neo4j", c.dbName)
}
