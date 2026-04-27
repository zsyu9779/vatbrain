package pgvector

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

// Client wraps a pgxpool connection pool for vector operations.
type Client struct {
	pool *pgxpool.Pool
}

// Config holds connection parameters.
type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
	// MaxConns defaults to 20.
	MaxConns int32
}

// NewClient creates a new pgvector client and verifies connectivity.
func NewClient(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Port == 0 {
		cfg.Port = 5432
	}
	if cfg.MaxConns == 0 {
		cfg.MaxConns = 20
	}

	dsn := fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=disable",
		cfg.User, cfg.Password, cfg.Host, cfg.Port, cfg.Database,
	)

	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("pgvector: parse config: %w", err)
	}
	poolCfg.MaxConns = cfg.MaxConns

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("pgvector: create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("pgvector: ping: %w", err)
	}

	return &Client{pool: pool}, nil
}

// InsertEmbedding stores a vector with metadata.
func (c *Client) InsertEmbedding(
	ctx context.Context,
	memoryID string,
	embedding []float32,
	summaryText string,
	projectID string,
	language string,
	taskType string,
	metadata map[string]any,
) error {
	vec := pgvector.NewVector(embedding)
	_, err := c.pool.Exec(ctx, `
		INSERT INTO episodic_embeddings
			(id, memory_id, embedding, summary_text, project_id, language, task_type, metadata)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7)
	`, memoryID, vec, summaryText, projectID, language, taskType, metadata)
	if err != nil {
		return fmt.Errorf("pgvector: insert embedding: %w", err)
	}
	return nil
}

// SearchResult is a single row from a similarity search.
type SearchResult struct {
	MemoryID    string
	SummaryText string
	Similarity  float64
	Metadata    map[string]any
}

// SimilaritySearch returns the top_k most similar embeddings.
// If filterIDs is non-empty, restricts the search to those memory_ids.
func (c *Client) SimilaritySearch(
	ctx context.Context,
	embedding []float32,
	topK int,
	filterIDs []string,
) ([]SearchResult, error) {
	vec := pgvector.NewVector(embedding)

	var rows pgx.Rows
	var err error

	if len(filterIDs) > 0 {
		rows, err = c.pool.Query(ctx, `
			SELECT memory_id, summary_text, metadata,
			       1 - (embedding <=> $1) AS similarity
			FROM episodic_embeddings
			WHERE memory_id = ANY($2)
			ORDER BY embedding <=> $1
			LIMIT $3
		`, vec, filterIDs, topK)
	} else {
		rows, err = c.pool.Query(ctx, `
			SELECT memory_id, summary_text, metadata,
			       1 - (embedding <=> $1) AS similarity
			FROM episodic_embeddings
			ORDER BY embedding <=> $1
			LIMIT $2
		`, vec, topK)
	}
	if err != nil {
		return nil, fmt.Errorf("pgvector: similarity search: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.MemoryID, &r.SummaryText, &r.Metadata, &r.Similarity); err != nil {
			return nil, fmt.Errorf("pgvector: scan row: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// GetEmbedding retrieves the raw embedding vector for a memory_id.
func (c *Client) GetEmbedding(ctx context.Context, memoryID string) ([]float32, error) {
	var vec pgvector.Vector
	err := c.pool.QueryRow(ctx,
		`SELECT embedding FROM episodic_embeddings WHERE memory_id = $1`, memoryID,
	).Scan(&vec)
	if err != nil {
		return nil, fmt.Errorf("pgvector: get embedding: %w", err)
	}
	return vec.Slice(), nil
}

// DeleteByMemoryID removes embeddings for a given memory_id.
func (c *Client) DeleteByMemoryID(ctx context.Context, memoryID string) error {
	_, err := c.pool.Exec(ctx,
		`DELETE FROM episodic_embeddings WHERE memory_id = $1`, memoryID,
	)
	return err
}

// HealthCheck verifies the pool can reach the database.
func (c *Client) HealthCheck(ctx context.Context) error {
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return c.pool.Ping(checkCtx)
}

// Close shuts down the connection pool.
func (c *Client) Close() {
	c.pool.Close()
}
