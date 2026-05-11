package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/vatbrain/vatbrain/internal/app"
	"github.com/vatbrain/vatbrain/internal/watcher/adapters"
)

type configureAdapterOutput struct {
	ConfigPath string `json:"config_path"`
	Adapter    string `json:"adapter"`
	Created    bool   `json:"created"`
}

func configureAdapterTool(a *app.App) server.ServerTool {
	tool := mcp.NewTool("configure_adapter",
		mcp.WithDescription("Create or update a custom agent memory adapter configuration. "+
			"The adapter will be active on the next poll cycle."),
		mcp.WithString("name", mcp.Required(),
			mcp.Description("Unique adapter name")),
		mcp.WithString("watch_path", mcp.Required(),
			mcp.Description("File pattern to watch (e.g. ~/my-agent/memory/*.md)")),
		mcp.WithString("format_type", mcp.Required(),
			mcp.Description("Memory format type"),
			mcp.Enum("yaml_frontmatter", "json_lines", "raw_text")),
		mcp.WithString("content_field", mcp.Required(),
			mcp.Description("Field name for RawMemory.Content")),
		mcp.WithString("project_id_field",
			mcp.Description("Field name for project ID (optional)")),
		mcp.WithString("session_id_field",
			mcp.Description("Field name for session ID (optional)")),
	)

	return server.ServerTool{
		Tool: tool,
		Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if a.MemoryWatcher == nil {
				return mcp.NewToolResultError("MemoryWatcher is not enabled. Set VATBRAIN_WATCHER_ENABLED=true"), nil
			}

			name, err := req.RequireString("name")
			if err != nil {
				return mcp.NewToolResultError("name is required"), nil
			}
			watchPath, err := req.RequireString("watch_path")
			if err != nil {
				return mcp.NewToolResultError("watch_path is required"), nil
			}
			formatType, err := req.RequireString("format_type")
			if err != nil {
				return mcp.NewToolResultError("format_type is required"), nil
			}
			contentField, err := req.RequireString("content_field")
			if err != nil {
				return mcp.NewToolResultError("content_field is required"), nil
			}
			projectIDField := req.GetString("project_id_field", "")
			sessionIDField := req.GetString("session_id_field", "")

			cfg := adapters.CustomProviderConfig{
				Name:        name,
				Description: fmt.Sprintf("Custom adapter: %s", name),
				Enabled:     true,
				Watch: adapters.WatchConfig{
					Paths: []string{watchPath},
				},
				Format: adapters.FormatConfig{
					Type: formatType,
					FieldMappings: adapters.FieldMappings{
						Content:   contentField,
						ProjectID: projectIDField,
						SessionID: sessionIDField,
					},
				},
			}

			configDir := a.Config.Watcher.AdapterConfigDir
			if configDir == "" {
				if home, err := os.UserHomeDir(); err == nil {
					configDir = filepath.Join(home, ".vatbrain", "adapters")
				}
			}

			if err := os.MkdirAll(configDir, 0o755); err != nil {
				return mcp.NewToolResultError("failed to create config dir: " + err.Error()), nil
			}

			configPath := filepath.Join(configDir, name+".yaml")
			data, err := yaml.Marshal(cfg)
			if err != nil {
				return mcp.NewToolResultError("failed to marshal config: " + err.Error()), nil
			}

			if err := os.WriteFile(configPath, data, 0o644); err != nil {
				return mcp.NewToolResultError("failed to write config: " + err.Error()), nil
			}

			// Register the new provider in the watcher registry.
			provider, err := adapters.NewCustomProvider(configPath)
			if err != nil {
				return mcp.NewToolResultError("failed to create provider: " + err.Error()), nil
			}
			if err := a.MemoryWatcher.Registry().Register(provider); err != nil {
				return mcp.NewToolResultError("failed to register provider: " + err.Error()), nil
			}

			out := configureAdapterOutput{
				ConfigPath: configPath,
				Adapter:    name,
				Created:    true,
			}
			resp, jErr := mcp.NewToolResultJSON(out)
			if jErr != nil {
				return mcp.NewToolResultError(jErr.Error()), nil
			}
			return resp, nil
		},
	}
}
