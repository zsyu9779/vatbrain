package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Client wraps a redis client with JSON helpers.
type Client struct {
	rc *redis.Client
}

// Config holds Redis connection parameters.
type Config struct {
	Addr     string
	Password string
	DB       int
}

// NewClient creates a new Redis client and verifies connectivity.
func NewClient(ctx context.Context, cfg Config) (*Client, error) {
	rc := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	if err := rc.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis: ping: %w", err)
	}

	return &Client{rc: rc}, nil
}

// GetJSON reads a key and unmarshals it into dest.
// Returns redis.Nil when the key does not exist.
func (c *Client) GetJSON(ctx context.Context, key string, dest any) error {
	data, err := c.rc.Get(ctx, key).Bytes()
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dest)
}

// SetJSON marshals value and stores it under key with optional TTL.
// A zero TTL means no expiration.
func (c *Client) SetJSON(ctx context.Context, key string, value any, ttl time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("redis: marshal: %w", err)
	}
	return c.rc.Set(ctx, key, data, ttl).Err()
}

// SortedSet helpers for context-weighted candidate sets.

// ZAdd adds members with scores to a sorted set.
func (c *Client) ZAdd(ctx context.Context, key string, members ...redis.Z) error {
	return c.rc.ZAdd(ctx, key, members...).Err()
}

// ZRangeByScore returns members within the given score range.
func (c *Client) ZRangeByScore(ctx context.Context, key string, min, max string) ([]string, error) {
	return c.rc.ZRangeByScore(ctx, key, &redis.ZRangeBy{
		Min: min,
		Max: max,
	}).Result()
}

// ZRemRangeByScore removes members within the given score range.
func (c *Client) ZRemRangeByScore(ctx context.Context, key string, min, max string) error {
	return c.rc.ZRemRangeByScore(ctx, key, min, max).Err()
}

// Del removes one or more keys.
func (c *Client) Del(ctx context.Context, keys ...string) error {
	return c.rc.Del(ctx, keys...).Err()
}

// Expire sets a TTL on a key.
func (c *Client) Expire(ctx context.Context, key string, ttl time.Duration) error {
	return c.rc.Expire(ctx, key, ttl).Err()
}

// LPush inserts values at the head of a list.
func (c *Client) LPush(ctx context.Context, key string, values ...any) error {
	return c.rc.LPush(ctx, key, values...).Err()
}

// LTrim trims a list to the specified range.
func (c *Client) LTrim(ctx context.Context, key string, start, stop int64) error {
	return c.rc.LTrim(ctx, key, start, stop).Err()
}

// LRange returns the specified elements of a list.
func (c *Client) LRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	return c.rc.LRange(ctx, key, start, stop).Result()
}

// HealthCheck verifies the client can reach Redis.
func (c *Client) HealthCheck(ctx context.Context) error {
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return c.rc.Ping(checkCtx).Err()
}

// Close shuts down the Redis client.
func (c *Client) Close() error {
	return c.rc.Close()
}
