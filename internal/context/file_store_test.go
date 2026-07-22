package contextmanager

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestFileStoreSummaryCheckpointSkipsCoveredMessages(t *testing.T) {
	store, err := NewFileConversationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const sessionID = "session-1"
	for _, message := range []StoredMessage{
		{ID: "m1", SessionID: sessionID, TurnID: "t1", Role: "user", Content: "one"},
		{ID: "m2", SessionID: sessionID, TurnID: "t2", Role: "assistant", Content: "two"},
		{ID: "m3", SessionID: sessionID, TurnID: "t3", Role: "user", Content: "three"},
	} {
		if err := store.AppendMessage(ctx, message); err != nil {
			t.Fatal(err)
		}
	}

	summary := SummarySnapshot{
		Version:                 1,
		SessionID:               sessionID,
		CoveredThroughMessageID: "m2",
		CoveredThroughTurnID:    "t2",
		Content:                 "summary",
	}
	if err := store.CommitSummary(ctx, summary, 0); err != nil {
		t.Fatal(err)
	}

	active, err := store.ActiveSummary(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if active == nil || active.CoveredThroughMessageID != "m2" {
		t.Fatalf("active summary = %#v", active)
	}
	messages, err := store.ListMessagesAfter(ctx, sessionID, active.CoveredThroughMessageID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].ID != "m3" {
		t.Fatalf("messages after checkpoint = %#v", messages)
	}
}

func TestFileStoreCommitSummaryRejectsStaleVersion(t *testing.T) {
	store, err := NewFileConversationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first := SummarySnapshot{Version: 1, SessionID: "session-1", Content: "one"}
	if err := store.CommitSummary(ctx, first, 0); err != nil {
		t.Fatal(err)
	}
	second := SummarySnapshot{Version: 2, SessionID: "session-1", Content: "two"}
	if err := store.CommitSummary(ctx, second, 0); !errors.Is(err, ErrSummaryVersionConflict) {
		t.Fatalf("error = %v, want ErrSummaryVersionConflict", err)
	}
	active, err := store.ActiveSummary(ctx, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if active.Version != 1 || active.Content != "one" {
		t.Fatalf("active summary changed: %#v", active)
	}
}

func TestFileStoreSavesAndVerifiesToolArtifact(t *testing.T) {
	store, err := NewFileConversationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	artifact := ToolArtifact{ID: "a1", SessionID: "session-1", ToolUseID: "call-1", ToolName: "Bash"}
	if err := store.SaveToolArtifact(context.Background(), artifact, strings.NewReader("complete output")); err != nil {
		t.Fatal(err)
	}
	metadata, body, err := store.LoadToolArtifact(context.Background(), "session-1", "a1")
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	content, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "complete output" || metadata.ContentSHA256 == "" {
		t.Fatalf("artifact = %#v, content = %q", metadata, content)
	}
}

func TestFileStoreRejectsUnsafeIdentifiers(t *testing.T) {
	store, err := NewFileConversationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	err = store.AppendMessage(context.Background(), StoredMessage{ID: "m1", SessionID: "../escape"})
	if !errors.Is(err, ErrInvalidIdentifier) {
		t.Fatalf("error = %v, want ErrInvalidIdentifier", err)
	}
}
