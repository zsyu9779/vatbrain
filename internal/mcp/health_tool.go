package mcp

import (
	"context"
	"log/slog"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/vatbrain/vatbrain/internal/app"
	"golang.org/x/sync/errgroup"
)

func healthCheckTool(a *app.App) server.ServerTool {
	tool := mcp.NewTool("health_check",
		mcp.WithDescription("Check the health status of all 4 database backends "+
			"(Neo4j, pgvector, Redis, MinIO). Returns 'healthy' if all are up."),
	)

	return server.ServerTool{
		Tool: tool,
		Handler: func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var neo4jOK, pgOK, redisOK, minioOK bool
			g, gCtx := errgroup.WithContext(ctx)

			g.Go(func() error {
				neo4jOK = a.Neo4j != nil && a.Neo4j.HealthCheck(gCtx) == nil
				if !neo4jOK {
					slog.Warn("health neo4j degraded")
				}
				return nil
			})
			g.Go(func() error {
				pgOK = a.Pgvector != nil && a.Pgvector.HealthCheck(gCtx) == nil
				if !pgOK {
					slog.Warn("health pgvector degraded")
				}
				return nil
			})
			g.Go(func() error {
				redisOK = a.Redis != nil && a.Redis.HealthCheck(gCtx) == nil
				if !redisOK {
					slog.Warn("health redis degraded")
				}
				return nil
			})
			g.Go(func() error {
				minioOK = a.Minio != nil && a.Minio.HealthCheck(gCtx) == nil
				if !minioOK {
					slog.Warn("health minio degraded")
				}
				return nil
			})

			_ = g.Wait()

			if neo4jOK && pgOK && redisOK && minioOK {
				resp, e := mcp.NewToolResultJSON(map[string]string{"status": "healthy"})
				if e != nil {
					return mcp.NewToolResultError(e.Error()), nil
				}
				return resp, nil
			}

			var down []string
			if !neo4jOK {
				down = append(down, "neo4j")
			}
			if !pgOK {
				down = append(down, "pgvector")
			}
			if !redisOK {
				down = append(down, "redis")
			}
			if !minioOK {
				down = append(down, "minio")
			}

			resp, e := mcp.NewToolResultJSON(map[string]string{
				"status":  "degraded",
				"message": "unhealthy: " + strings.Join(down, ", "),
			})
			if e != nil {
				return mcp.NewToolResultError(e.Error()), nil
			}
			return resp, nil
		},
	}
}
