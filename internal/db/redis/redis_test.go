package redis

import (
	"context"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustConnect(t *testing.T) *Client {
	t.Helper()
	c, err := NewClient(context.Background(), Config{
		Addr: "localhost:6379",
	})
	if err != nil {
		t.Skipf("skipping integration test: redis not available: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestRedis_NewClient_And_HealthCheck(t *testing.T) {
	c := mustConnect(t)
	ctx := context.Background()
	assert.NoError(t, c.HealthCheck(ctx))
}

func TestRedis_SetJSON_GetJSON(t *testing.T) {
	c := mustConnect(t)
	ctx := context.Background()

	type testData struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	err := c.SetJSON(ctx, "test:setget", testData{Name: "hello", Value: 42}, time.Minute)
	require.NoError(t, err)

	var got testData
	err = c.GetJSON(ctx, "test:setget", &got)
	require.NoError(t, err)
	assert.Equal(t, "hello", got.Name)
	assert.Equal(t, 42, got.Value)

	c.Del(ctx, "test:setget")
}

func TestRedis_GetJSON_Missing(t *testing.T) {
	c := mustConnect(t)
	ctx := context.Background()

	var v string
	err := c.GetJSON(ctx, "nonexistent:key", &v)
	assert.Error(t, err)
}

func TestRedis_ZAdd_ZRangeByScore(t *testing.T) {
	c := mustConnect(t)
	ctx := context.Background()

	err := c.ZAdd(ctx, "test:zset", goredis.Z{Score: 1.0, Member: "a"},
		goredis.Z{Score: 2.0, Member: "b"},
		goredis.Z{Score: 3.0, Member: "c"})
	require.NoError(t, err)

	members, err := c.ZRangeByScore(ctx, "test:zset", "1", "2")
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, members)

	c.Del(ctx, "test:zset")
}

func TestRedis_ZRemRangeByScore(t *testing.T) {
	c := mustConnect(t)
	ctx := context.Background()

	c.ZAdd(ctx, "test:zrem", goredis.Z{Score: 1.0, Member: "x"}, goredis.Z{Score: 5.0, Member: "y"})
	err := c.ZRemRangeByScore(ctx, "test:zrem", "4", "10")
	require.NoError(t, err)

	members, _ := c.ZRangeByScore(ctx, "test:zrem", "0", "2")
	assert.Equal(t, []string{"x"}, members)

	c.Del(ctx, "test:zrem")
}

func TestRedis_LPush_LRange_LTrim(t *testing.T) {
	c := mustConnect(t)
	ctx := context.Background()

	err := c.LPush(ctx, "test:list", "c", "b", "a")
	require.NoError(t, err)

	items, err := c.LRange(ctx, "test:list", 0, -1)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "c"}, items)

	err = c.LTrim(ctx, "test:list", 0, 1)
	require.NoError(t, err)

	items, _ = c.LRange(ctx, "test:list", 0, -1)
	assert.Equal(t, []string{"a", "b"}, items)

	c.Del(ctx, "test:list")
}

func TestRedis_SetNX_Expire(t *testing.T) {
	c := mustConnect(t)
	ctx := context.Background()

	ok, err := c.SetNX(ctx, "test:setnx", "locked", time.Minute)
	require.NoError(t, err)
	assert.True(t, ok)

	// Second call should fail, key exists
	ok, err = c.SetNX(ctx, "test:setnx", "locked-again", time.Minute)
	require.NoError(t, err)
	assert.False(t, ok)

	// Set expire and verify
	err = c.Expire(ctx, "test:setnx", time.Second)
	require.NoError(t, err)

	c.Del(ctx, "test:setnx")
}

func TestRedis_Close(t *testing.T) {
	c := mustConnect(t)
	assert.NoError(t, c.Close())
}
