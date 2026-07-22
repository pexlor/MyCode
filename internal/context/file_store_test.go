package contextmanager

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestFileStoreSessionLifecycle(t *testing.T) {
	root := t.TempDir()
	store, err := NewFileConversationStore(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	created := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	metadata := SessionMetadata{ID: "session-one", Title: "first", Workspace: "/repo", CreatedAt: created}
	if err := store.CreateSession(ctx, metadata); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessage(ctx, StoredMessage{ID: "m1", SessionID: metadata.ID, Role: "user", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetSession(ctx, metadata.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "first" || got.Workspace != "/repo" || got.MessageCount != 1 || got.FormatVersion != storeFormatVersion {
		t.Fatalf("metadata = %#v", got)
	}
	if err := store.RenameSession(ctx, metadata.ID, "renamed"); err != nil {
		t.Fatal(err)
	}
	got, _ = store.GetSession(ctx, metadata.ID)
	if got.Title != "renamed" {
		t.Fatalf("title = %q", got.Title)
	}
	if err := store.DeleteSession(ctx, metadata.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, metadata.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("session directory still exists: %v", err)
	}
}

func TestFileStoreListSessionsFiltersWorkspaceAndSorts(t *testing.T) {
	store, err := NewFileConversationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, metadata := range []SessionMetadata{
		{ID: "old", Title: "old", Workspace: "/repo", UpdatedAt: time.Unix(1, 0)},
		{ID: "other", Title: "other", Workspace: "/else", UpdatedAt: time.Unix(3, 0)},
		{ID: "new", Title: "new", Workspace: "/repo", UpdatedAt: time.Unix(2, 0)},
	} {
		if err := store.CreateSession(ctx, metadata); err != nil {
			t.Fatal(err)
		}
	}
	items, err := store.ListSessions(ctx, "/repo", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID != "new" || items[1].ID != "old" {
		t.Fatalf("sessions = %#v", items)
	}
}

func TestFileStoreDeleteSessionRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	store, err := NewFileConversationStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteSession(context.Background(), "linked"); !errors.Is(err, ErrUnsafeSessionPath) {
		t.Fatalf("error = %v, want ErrUnsafeSessionPath", err)
	}
}
