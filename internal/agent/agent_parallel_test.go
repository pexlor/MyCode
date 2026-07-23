package agent

import (
	"MyCode/internal/llm"
	"MyCode/internal/message"
	"MyCode/internal/permission"
	"MyCode/internal/tool"
	"context"
	"sync"
	"testing"
	"time"
)

func TestAgentExecutesToolCallsConcurrently(t *testing.T) {
	const callCount = 3
	const executionTime = 100 * time.Millisecond

	manager := tool.NewToolsManager()
	policy := permission.DefaultPolicy(t.TempDir())
	policy.Tools["parallel_test"] = permission.ToolPolicy{Permission: permission.Allow}
	permissions, err := permission.NewManager(policy)
	if err != nil {
		t.Fatal(err)
	}
	manager.SetPermissionManager(permissions)
	parallelTool := &concurrentTestTool{delay: executionTime}
	manager.RegisterTool(parallelTool)

	runner, err := NewAgent(context.Background(), &parallelTestClient{callCount: callCount}, manager)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	var resultIDs []string
	var executionStartIDs []string
	var usage llm.UsageInfo
	for event := range runner.Run(&message.MessageManager{SystemPrompt: "test"}) {
		if start, ok := event.(ToolExecutionStartEvent); ok {
			executionStartIDs = append(executionStartIDs, start.ToolUseID)
		}
		if result, ok := event.(ToolResultEvent); ok {
			resultIDs = append(resultIDs, result.ToolUseID)
		}
		if done, ok := event.(DoneEvent); ok {
			usage = done.Usage
		}
	}
	elapsed := time.Since(started)

	if parallelTool.maxRunning() < 2 {
		t.Fatal("tool calls did not overlap")
	}
	if elapsed >= 2*executionTime {
		t.Fatalf("tool calls took %s; expected concurrent execution", elapsed)
	}
	if len(resultIDs) != callCount {
		t.Fatalf("received %d tool result events, want %d", len(resultIDs), callCount)
	}
	if len(executionStartIDs) != callCount {
		t.Fatalf("received %d tool execution start events, want %d", len(executionStartIDs), callCount)
	}
	if usage != (llm.UsageInfo{InputTokens: 30, OutputTokens: 7, TotalTokens: 37}) {
		t.Fatalf("usage = %#v, want input 30, output 7, total 37", usage)
	}
}

type concurrentTestTool struct {
	delay time.Duration
	mu    sync.Mutex
	run   int
	max   int
}

func (t *concurrentTestTool) Name() string        { return "parallel_test" }
func (t *concurrentTestTool) Description() string { return "parallel test tool" }
func (t *concurrentTestTool) Schema() *tool.ToolSchema {
	return &tool.ToolSchema{Name: t.Name()}
}
func (t *concurrentTestTool) Execute(_ context.Context, _ map[string]any) tool.ToolResult {
	t.mu.Lock()
	t.run++
	if t.run > t.max {
		t.max = t.run
	}
	t.mu.Unlock()
	time.Sleep(t.delay)
	t.mu.Lock()
	t.run--
	t.mu.Unlock()
	return tool.ToolResult{Output: "ok"}
}
func (t *concurrentTestTool) maxRunning() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.max
}

type parallelTestClient struct {
	callCount int
	mu        sync.Mutex
	calls     int
}

func (c *parallelTestClient) Stream(_ *llm.StreamRequest) (<-chan llm.StreamEvent, <-chan error) {
	c.mu.Lock()
	iteration := c.calls
	c.calls++
	c.mu.Unlock()
	events := make(chan llm.StreamEvent, c.callCount+1)
	errs := make(chan error)
	if iteration == 0 {
		for i := range c.callCount {
			events <- llm.ToolCallComplete{ToolID: string(rune('a' + i)), ToolName: "parallel_test", Arguments: map[string]any{}}
		}
		events <- llm.StreamEnd{StopReason: "tool_calls", Usage: llm.UsageInfo{InputTokens: 10, OutputTokens: 3, TotalTokens: 13}}
	} else {
		events <- llm.StreamEnd{StopReason: "stop", Usage: llm.UsageInfo{InputTokens: 20, OutputTokens: 4, TotalTokens: 24}}
	}
	close(events)
	close(errs)
	return events, errs
}
