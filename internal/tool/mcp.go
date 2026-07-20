package tool

import (
	"MyCode/internal/mcp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

var nonToolName = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

type MCPTool struct {
	name        string
	description string
	schema      map[string]any
	remoteName  string
	client      *mcp.Client
}

func (t *MCPTool) Name() string        { return t.name }
func (t *MCPTool) Description() string { return t.description }
func (t *MCPTool) Schema() *ToolSchema {
	return &ToolSchema{Name: t.name, Description: t.description, Parameters: t.schema}
}
func (t *MCPTool) Execute(ctx context.Context, args map[string]any) ToolResult {
	result, err := t.client.CallTool(ctx, t.remoteName, args)
	if err != nil {
		return ToolResult{Output: fmt.Sprintf("MCP tool %q failed: %v", t.name, err), IsError: true}
	}
	return ToolResult{Output: result.Text(), IsError: result.IsError}
}

// LoadMCPTools starts configured servers and returns their prefixed tools plus a
// cleanup function. Tool names use mcp_<server>_<tool> to avoid collisions.
func LoadMCPTools(ctx context.Context, configPath string) ([]Tool, func(), error) {
	config, err := mcp.LoadConfig(configPath)
	if err != nil {
		return nil, nil, err
	}
	names := make([]string, 0, len(config.Servers))
	for name := range config.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	clients := make([]*mcp.Client, 0, len(names))
	tools := make([]Tool, 0)
	closeAll := func() {
		for _, client := range clients {
			_ = client.Close()
		}
	}
	for _, serverName := range names {
		client, startErr := mcp.Start(ctx, serverName, config.Servers[serverName])
		if startErr != nil {
			closeAll()
			return nil, nil, startErr
		}
		clients = append(clients, client)
		remoteTools, listErr := client.ListTools(ctx)
		if listErr != nil {
			closeAll()
			return nil, nil, fmt.Errorf("list tools from MCP server %q: %w", serverName, listErr)
		}
		for _, remote := range remoteTools {
			if remote.Name == "" {
				closeAll()
				return nil, nil, fmt.Errorf("MCP server %q returned a tool without a name", serverName)
			}
			schema := remote.InputSchema
			if schema == nil {
				schema = map[string]any{"type": "object"}
			}
			tools = append(tools, &MCPTool{
				name:        mcpToolName(serverName, remote.Name),
				description: remote.Description,
				schema:      schema,
				remoteName:  remote.Name,
				client:      client,
			})
		}
	}
	return tools, closeAll, nil
}

func mcpToolName(server, remote string) string {
	return "mcp_" + nonToolName.ReplaceAllString(server, "_") + "_" + nonToolName.ReplaceAllString(remote, "_")
}

// CreateDefaultToolsWithMCP loads .agent/mcp.yaml if it exists. A malformed or
// unreachable explicitly configured server fails initialization rather than
// silently removing tools.
func CreateDefaultToolsWithMCP(ctx context.Context) (*ToolsManager, func(), error) {
	manager := CreateDefaultTools()
	workspace, err := filepath.Abs(".")
	if err != nil {
		return nil, nil, err
	}
	configPath := filepath.Join(workspace, ".agent", "mcp.yaml")
	if _, err := os.Stat(configPath); err != nil {
		if os.IsNotExist(err) {
			return manager, func() {}, nil
		}
		return nil, nil, err
	}
	mcpTools, closeAll, err := LoadMCPTools(ctx, configPath)
	if err != nil {
		return nil, nil, err
	}
	for _, registered := range mcpTools {
		if manager.GetTool(registered.Name()) != nil {
			closeAll()
			return nil, nil, fmt.Errorf("MCP tool name collision: %q", registered.Name())
		}
		manager.RegisterTool(registered)
	}
	return manager, closeAll, nil
}
