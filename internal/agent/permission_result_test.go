package agent

import (
	"MyCode/internal/llm"
	"MyCode/internal/message"
	"MyCode/internal/permission"
	"MyCode/internal/tool"
	"context"
	"strings"
	"sync"
	"testing"
)

func TestAgentReturnsPermissionDenialToModel(t *testing.T) {
	manager := tool.NewToolsManager()
	blocked := &permissionResultTestTool{}
	manager.RegisterTool(blocked)
	manager.SetPermissionManager(permissionResultTestManager{})

	client := &permissionResultTestClient{}
	runner, err := NewAgent(context.Background(), client, manager)
	if err != nil {
		t.Fatal(err)
	}
	for range runner.Run(&message.MessageManager{SystemPrompt: "test"}) {
	}

	if blocked.called {
		t.Fatal("permission-denied tool was executed")
	}
	request := client.secondRequest()
	if request == nil {
		t.Fatal("model was not called after the denied tool result")
	}
	if len(request.Messages) != 2 {
		t.Fatalf("messages = %#v, want assistant tool use and tool result", request.Messages)
	}
	result := request.Messages[1]
	if result.Role != message.TOOL || len(result.ToolResults) != 1 {
		t.Fatalf("tool result message = %#v", result)
	}
	if !result.ToolResults[0].IsError || !strings.Contains(result.ToolResults[0].Content, "permission deny: blocked by test policy") {
		t.Fatalf("denial result = %#v", result.ToolResults[0])
	}
}

type permissionResultTestManager struct{}

func (permissionResultTestManager) Authorize(context.Context, permission.PermissionRequest) (permission.PermissionResult, error) {
	return permission.PermissionResult{Decision: permission.Deny, Reason: "blocked by test policy"}, nil
}

type permissionResultTestTool struct{ called bool }

func (t *permissionResultTestTool) Name() string        { return "blocked_test" }
func (t *permissionResultTestTool) Description() string { return "blocked test tool" }
func (t *permissionResultTestTool) Schema() *tool.ToolSchema {
	return &tool.ToolSchema{Name: t.Name()}
}
func (t *permissionResultTestTool) Execute(context.Context, map[string]any) tool.ToolResult {
	t.called = true
	return tool.ToolResult{Output: "should not run"}
}

type permissionResultTestClient struct {
	mu       sync.Mutex
	requests []*llm.StreamRequest
}

func (c *permissionResultTestClient) Stream(request *llm.StreamRequest) (<-chan llm.StreamEvent, <-chan error) {
	c.mu.Lock()
	iteration := len(c.requests)
	c.requests = append(c.requests, request)
	c.mu.Unlock()

	events := make(chan llm.StreamEvent, 2)
	errs := make(chan error)
	if iteration == 0 {
		events <- llm.ToolCallComplete{ToolID: "blocked-call", ToolName: "blocked_test", Arguments: map[string]any{}}
		events <- llm.StreamEnd{StopReason: "tool_calls"}
	} else {
		events <- llm.StreamEnd{StopReason: "stop"}
	}
	close(events)
	close(errs)
	return events, errs
}

func (c *permissionResultTestClient) secondRequest() *llm.StreamRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.requests) < 2 {
		return nil
	}
	return c.requests[1]
}
