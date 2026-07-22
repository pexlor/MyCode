package contextmanager

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestOffloaderArchivesOversizedResult(t *testing.T) {
	store, err := NewFileConversationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	offloader := ResultOffloader{Store: store, Estimator: ConservativeEstimator{}, SingleLimit: 20, BatchLimit: 40}
	large := strings.Repeat("large output ", 30)
	messages := []StoredMessage{{
		ID: "m1", SessionID: "session-1", TurnID: "turn-1", Role: "tool",
		ToolResults: []StoredToolResult{{ToolUseID: "call-1", ToolName: "Bash", Content: large, State: ResultFull}},
	}}
	got, err := offloader.Process(context.Background(), "session-1", messages)
	if err != nil {
		t.Fatal(err)
	}
	result := got[0].ToolResults[0]
	if result.State != ResultReference || result.ArtifactID == "" {
		t.Fatalf("result = %#v", result)
	}
	if strings.Contains(result.Content, large) {
		t.Fatal("full output leaked into context view")
	}
	_, body, err := store.LoadToolArtifact(context.Background(), "session-1", result.ArtifactID)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	content, _ := io.ReadAll(body)
	if string(content) != large {
		t.Fatalf("artifact content length = %d, want %d", len(content), len(large))
	}
}

func TestOffloaderLeavesSmallResultInline(t *testing.T) {
	store, _ := NewFileConversationStore(t.TempDir())
	offloader := ResultOffloader{Store: store, Estimator: ConservativeEstimator{}, SingleLimit: 100, BatchLimit: 200}
	messages := []StoredMessage{{ID: "m1", SessionID: "s1", ToolResults: []StoredToolResult{{ToolUseID: "c1", Content: "ok", State: ResultFull}}}}
	got, err := offloader.Process(context.Background(), "s1", messages)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].ToolResults[0].State != ResultFull || got[0].ToolResults[0].Content != "ok" {
		t.Fatalf("small result changed: %#v", got[0].ToolResults[0])
	}
}
