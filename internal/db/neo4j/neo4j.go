package neo4j

import (
	"context"
	"fmt"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// Client wraps the Neo4j driver with session management.
type Client struct {
	driver neo4j.DriverWithContext
	dbName string
}

// Config holds Neo4j connection parameters.
type Config struct {
	URI      string
	Username string
	Password string
	Database string
	// MaxConnectionPoolSize defaults to 100.
	MaxConnectionPoolSize int
}

// NewClient creates a new Neo4j client and verifies connectivity.
func NewClient(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Database == "" {
		cfg.Database = "neo4j"
	}
	if cfg.MaxConnectionPoolSize == 0 {
		cfg.MaxConnectionPoolSize = 100
	}

	driver, err := neo4j.NewDriverWithContext(
		cfg.URI,
		neo4j.BasicAuth(cfg.Username, cfg.Password, ""),
		func(c *neo4j.Config) {
			c.MaxConnectionPoolSize = cfg.MaxConnectionPoolSize
		},
	)
	if err != nil {
		return nil, fmt.Errorf("neo4j: create driver: %w", err)
	}

	c := &Client{driver: driver, dbName: cfg.Database}

	if err := driver.VerifyConnectivity(ctx); err != nil {
		return nil, fmt.Errorf("neo4j: verify connectivity: %w", err)
	}

	return c, nil
}

// Session returns a new session configured for the target database.
func (c *Client) Session(ctx context.Context) neo4j.SessionWithContext {
	return c.driver.NewSession(ctx, neo4j.SessionConfig{
		DatabaseName: c.dbName,
	})
}

// ExecuteRead runs a read transaction.
func (c *Client) ExecuteRead(
	ctx context.Context,
	fn func(tx neo4j.ManagedTransaction) (any, error),
) (any, error) {
	session := c.Session(ctx)
	defer session.Close(ctx)
	return session.ExecuteRead(ctx, fn)
}

// ExecuteWrite runs a write transaction.
func (c *Client) ExecuteWrite(
	ctx context.Context,
	fn func(tx neo4j.ManagedTransaction) (any, error),
) (any, error) {
	session := c.Session(ctx)
	defer session.Close(ctx)
	return session.ExecuteWrite(ctx, fn)
}

// HealthCheck verifies the driver can reach the database.
func (c *Client) HealthCheck(ctx context.Context) error {
	verifyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return c.driver.VerifyConnectivity(verifyCtx)
}

// Close shuts down the driver.
func (c *Client) Close(ctx context.Context) error {
	return c.driver.Close(ctx)
}
