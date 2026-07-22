package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	contextmanager "MyCode/internal/context"
	"MyCode/internal/llm"
	"MyCode/internal/message"
	"MyCode/internal/tool"
)

func TestAgentUsesContextManagerView(t *testing.T) {
	store, _ := contextmanager.NewFileConversationStore(t.TempDir())
	manager, err := contextmanager.NewContextManager(contextmanager.ContextManagerConfig{
		Store: store, Estimator: contextmanager.ConservativeEstimator{}, Policy: contextmanager.DefaultPolicy(),
		Model: contextmanager.ModelContextSpec{ModelName: "test", ContextWindow: 100_000, MaxOutputTokens: 1000},
	})
	if err != nil {
		t.Fatal(err)
	}
	client := &contextCaptureClient{}
	runner, err := NewAgent(context.Background(), client, tool.NewToolsManager())
	if err != nil {
		t.Fatal(err)
	}
	runner.SetContextManager(manager, "session-1")
	messages := &message.MessageManager{SystemPrompt: "system"}
	messages.AddText("first request")
	for range runner.Run(messages) {
	}
	messages.AddText("second request")
	for range runner.Run(messages) {
	}
	if len(client.requests) != 2 {
		t.Fatalf("requests = %d", len(client.requests))
	}
	if !requestContains(client.requests[1], "first response") || !requestContains(client.requests[1], "second request") {
		t.Fatalf("second request history = %#v", client.requests[1].Messages)
	}
	stored, err := store.ListMessages(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 4 || stored[3].Content != "second response" {
		t.Fatalf("stored messages = %#v", stored)
	}
}

type contextCaptureClient struct {
	mu       sync.Mutex
	requests []*llm.StreamRequest
}

func (c *contextCaptureClient) Stream(request *llm.StreamRequest) (<-chan llm.StreamEvent, <-chan error) {
	c.mu.Lock()
	c.requests = append(c.requests, request)
	call := len(c.requests)
	c.mu.Unlock()
	events := make(chan llm.StreamEvent, 2)
	errs := make(chan error)
	events <- llm.TextStream{Text: map[int]string{1: "first response", 2: "second response"}[call]}
	events <- llm.StreamEnd{StopReason: "stop"}
	close(events)
	close(errs)
	return events, errs
}

func requestContains(request *llm.StreamRequest, value string) bool {
	for _, item := range request.Messages {
		if strings.Contains(item.Content, value) {
			return true
		}
	}
	return false
}
