package agent

import (
	"MyCode/internal/llm"
	"MyCode/internal/message"
	"MyCode/internal/tool"
	"context"
	"testing"
	"time"
)

func TestAgentRunContextStopsAnInFlightRequest(t *testing.T) {
	runner, err := NewAgent(context.Background(), blockingStreamClient{}, tool.NewToolsManager())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	events := runner.RunContext(ctx, &message.MessageManager{SystemPrompt: "test"})
	select {
	case event := <-events:
		if _, ok := event.(ThinkingStartEvent); !ok {
			t.Fatalf("first event = %T, want ThinkingStartEvent", event)
		}
	case <-time.After(time.Second):
		t.Fatal("agent did not start")
	}
	cancel()
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("agent emitted an event after cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("agent did not stop after cancellation")
	}
}

type blockingStreamClient struct{}

func (blockingStreamClient) Stream(request *llm.StreamRequest) (<-chan llm.StreamEvent, <-chan error) {
	events := make(chan llm.StreamEvent)
	errors := make(chan error)
	go func() {
		<-request.Context.Done()
		close(events)
		close(errors)
	}()
	return events, errors
}
