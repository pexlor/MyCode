package contextmanager

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"MyCode/internal/message"
)

func TestActivePathsIncludesOnlySuccessfulToolCalls(t *testing.T) {
	messages := []StoredMessage{
		{
			Role: message.ASSISTANT,
			ToolUses: []StoredToolUse{
				{ToolUseID: "failed", ToolName: "ReadFile", Arguments: map[string]any{"file_path": "/outside/secret.go"}},
				{ToolUseID: "successful", ToolName: "ReadFile", Arguments: map[string]any{"file_path": "internal/context/manager.go"}},
				{ToolUseID: "pending", ToolName: "ReadFile", Arguments: map[string]any{"file_path": "/outside/pending.go"}},
			},
		},
		{
			Role: message.TOOL,
			ToolResults: []StoredToolResult{
				{ToolUseID: "failed", IsError: true},
				{ToolUseID: "successful", IsError: false},
			},
		},
	}

	got := activePaths(messages)
	want := []string{"internal/context/manager.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("activePaths() = %#v, want %#v", got, want)
	}
}

func TestContextManagerBuildStartsAfterActiveCheckpoint(t *testing.T) {
	store, _ := NewFileConversationStore(t.TempDir())
	ctx := context.Background()
	for _, item := range []StoredMessage{
		{ID: "m1", SessionID: "s1", TurnID: "t1", Role: message.USER, Content: "covered secret", TurnStatus: TurnComplete},
		{ID: "m2", SessionID: "s1", TurnID: "t2", Role: message.ASSISTANT, Content: "covered answer", TurnStatus: TurnComplete},
		{ID: "m3", SessionID: "s1", TurnID: "t3", Role: message.USER, Content: "new request", TurnStatus: TurnOpen},
	} {
		if err := store.AppendMessage(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.CommitSummary(ctx, SummarySnapshot{Version: 1, SessionID: "s1", CoveredThroughMessageID: "m2", CoveredThroughTurnID: "t2", Content: "active summary"}, 0); err != nil {
		t.Fatal(err)
	}
	manager, err := NewContextManager(ContextManagerConfig{
		Store: store, Estimator: ConservativeEstimator{}, Policy: DefaultPolicy(),
		Model: ModelContextSpec{ModelName: "test", ContextWindow: 100_000, MaxOutputTokens: 1000},
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := manager.Build(ctx, BuildInput{SessionID: "s1", SystemPrompt: "system"})
	if err != nil {
		t.Fatal(err)
	}
	rendered := view.SystemPrompt
	for _, item := range view.Messages {
		rendered += item.Content
	}
	if strings.Contains(rendered, "covered secret") || strings.Contains(rendered, "covered answer") {
		t.Fatalf("covered messages leaked into view: %q", rendered)
	}
	if !strings.Contains(rendered, "active summary") || !strings.Contains(rendered, "new request") {
		t.Fatalf("summary or new message missing: %q", rendered)
	}
}

func TestContextManagerBuildRejectsHardLimit(t *testing.T) {
	store, _ := NewFileConversationStore(t.TempDir())
	manager, err := NewContextManager(ContextManagerConfig{
		Store: store, Estimator: ConservativeEstimator{}, Policy: DefaultPolicy(),
		Model: ModelContextSpec{ModelName: "test", ContextWindow: 100, MaxOutputTokens: 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.Build(context.Background(), BuildInput{SessionID: "s1", SystemPrompt: strings.Repeat("system", 100)})
	if err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("error = %v, want budget error", err)
	}
}

func TestContextManagerResumedHistoryDoesNotDuplicateTranscript(t *testing.T) {
	store, _ := NewFileConversationStore(t.TempDir())
	ctx := context.Background()
	for _, item := range []StoredMessage{
		{ID: "message-000001", SessionID: "s1", TurnID: "turn-000001", Role: message.USER, Content: "old question"},
		{ID: "message-000002", SessionID: "s1", TurnID: "turn-000001", Role: message.ASSISTANT, Content: "old answer", TurnStatus: TurnComplete},
	} {
		if err := store.AppendMessage(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := NewContextManager(ContextManagerConfig{Store: store, Estimator: ConservativeEstimator{}, Policy: DefaultPolicy(), Model: ModelContextSpec{ModelName: "test", ContextWindow: 100000, MaxOutputTokens: 1000}})
	if err != nil {
		t.Fatal(err)
	}
	history := []message.Message{{Role: message.USER, Content: "old question"}, {Role: message.ASSISTANT, Content: "old answer"}, {Role: message.USER, Content: "new question"}}
	if _, err := manager.Build(ctx, BuildInput{SessionID: "s1", SystemPrompt: "system", History: history}); err != nil {
		t.Fatal(err)
	}
	stored, err := store.ListMessages(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 3 || stored[2].Content != "new question" {
		t.Fatalf("stored = %#v", stored)
	}
}
