package agent

import (
	"MyCode/internal/llm"
	"MyCode/internal/message"
	"MyCode/internal/permission"
	"MyCode/internal/tool"
	"context"
	"sync"
	"testing"
)

// TestAgentCanCallMCPTool verifies the same path used by registered MCP tools:
// schema exposure to the model, Agent dispatch, and permission-gated execution.
func TestAgentCanCallMCPTool(t *testing.T) {
	workspace := t.TempDir()
	policy := permission.DefaultPolicy(workspace)
	policy.Tools["mcp_demo_echo"] = permission.ToolPolicy{Permission: permission.Allow}
	permissions, err := permission.NewManager(policy)
	if err != nil {
		t.Fatal(err)
	}
	manager := tool.NewToolsManager()
	remote := &mcpTestTool{}
	manager.RegisterTool(remote)
	manager.SetPermissionManager(permissions)

	client := &mcpTestClient{}
	runner, err := NewAgent(context.Background(), client, manager)
	if err != nil {
		t.Fatal(err)
	}
	messages := &message.MessageManager{SystemPrompt: "test"}
	for range runner.Run(messages) {
	}
	if !remote.called {
		t.Fatal("MCP tool was not executed")
	}
	if !client.sawMCPTool() {
		t.Fatal("MCP tool schema was not sent to the model")
	}
}

type mcpTestTool struct{ called bool }

func (t *mcpTestTool) Name() string        { return "mcp_demo_echo" }
func (t *mcpTestTool) Description() string { return "MCP echo tool" }
func (t *mcpTestTool) Schema() *tool.ToolSchema {
	return &tool.ToolSchema{Name: t.Name(), Description: t.Description(), Parameters: map[string]any{"type": "object"}}
}
func (t *mcpTestTool) Execute(context.Context, map[string]any) tool.ToolResult {
	t.called = true
	return tool.ToolResult{Output: "called"}
}

type mcpTestClient struct {
	mu       sync.Mutex
	requests []*llm.StreamRequest
}

func (c *mcpTestClient) Stream(request *llm.StreamRequest) (<-chan llm.StreamEvent, <-chan error) {
	c.mu.Lock()
	call := len(c.requests)
	c.requests = append(c.requests, request)
	c.mu.Unlock()
	events := make(chan llm.StreamEvent, 2)
	errs := make(chan error)
	if call == 0 {
		events <- llm.ToolCallComplete{ToolID: "mcp-call", ToolName: "mcp_demo_echo", Arguments: map[string]any{}}
		events <- llm.StreamEnd{StopReason: "tool_calls"}
	} else {
		events <- llm.StreamEnd{StopReason: "stop"}
	}
	close(events)
	close(errs)
	return events, errs
}

func (c *mcpTestClient) sawMCPTool() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.requests) == 0 {
		return false
	}
	for _, schema := range c.requests[0].Tools {
		if schema.Name == "mcp_demo_echo" {
			return true
		}
	}
	return false
}
