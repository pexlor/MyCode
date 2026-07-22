package session

import (
	contextmanager "MyCode/internal/context"
	"MyCode/internal/message"
	"context"
	"errors"
	"sort"
	"testing"
	"time"
)

type memoryStore struct {
	metadata  map[string]contextmanager.SessionMetadata
	messages  map[string][]contextmanager.StoredMessage
	renameErr error
}

func newMemoryStore() *memoryStore {
	return &memoryStore{metadata: map[string]contextmanager.SessionMetadata{}, messages: map[string][]contextmanager.StoredMessage{}}
}

func (s *memoryStore) CreateSession(_ context.Context, item contextmanager.SessionMetadata) error {
	if _, ok := s.metadata[item.ID]; ok {
		return contextmanager.ErrSessionExists
	}
	item.FormatVersion = 1
	s.metadata[item.ID] = item
	return nil
}
func (s *memoryStore) GetSession(_ context.Context, id string) (contextmanager.SessionMetadata, error) {
	item, ok := s.metadata[id]
	if !ok {
		return contextmanager.SessionMetadata{}, contextmanager.ErrSessionNotFound
	}
	return item, nil
}
func (s *memoryStore) ListSessions(_ context.Context, workspace string, limit int) ([]contextmanager.SessionMetadata, error) {
	var items []contextmanager.SessionMetadata
	for _, item := range s.metadata {
		if item.Workspace == workspace {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}
func (s *memoryStore) RenameSession(_ context.Context, id, title string) error {
	if s.renameErr != nil {
		return s.renameErr
	}
	item, ok := s.metadata[id]
	if !ok {
		return contextmanager.ErrSessionNotFound
	}
	item.Title = title
	s.metadata[id] = item
	return nil
}
func (s *memoryStore) DeleteSession(_ context.Context, id string) error {
	if _, ok := s.metadata[id]; !ok {
		return contextmanager.ErrSessionNotFound
	}
	delete(s.metadata, id)
	delete(s.messages, id)
	return nil
}
func (s *memoryStore) ListMessages(_ context.Context, id string) ([]contextmanager.StoredMessage, error) {
	return append([]contextmanager.StoredMessage(nil), s.messages[id]...), nil
}

func TestServiceStartsLazyAndPersistsFirstMessage(t *testing.T) {
	store := newMemoryStore()
	service, err := NewService(store, "/repo", "system")
	if err != nil {
		t.Fatal(err)
	}
	initial := service.Current()
	if initial.ID == "" || initial.Persisted {
		t.Fatalf("initial = %#v", initial)
	}
	if err := service.AddUserMessage(context.Background(), "  Fix   login tests\nwith details "); err != nil {
		t.Fatal(err)
	}
	current := service.Current()
	if !current.Persisted || current.Title != "Fix login tests" || len(current.Messages.History) != 1 {
		t.Fatalf("current = %#v", current)
	}
	if current.Messages.History[0].Role != message.USER {
		t.Fatalf("history = %#v", current.Messages.History)
	}
}

func TestServiceResumeLoadsHistoryAndSwitchesAtomically(t *testing.T) {
	store := newMemoryStore()
	store.metadata["session-target"] = contextmanager.SessionMetadata{ID: "session-target", Title: "target", Workspace: "/repo", MessageCount: 2}
	store.messages["session-target"] = []contextmanager.StoredMessage{
		{Role: message.USER, Content: "question"},
		{Role: message.ASSISTANT, Content: "answer", ToolUses: []contextmanager.StoredToolUse{{ToolUseID: "call-1", ToolName: "ReadFile"}}},
	}
	service, _ := NewService(store, "/repo", "current-system")
	before := service.Current().ID
	current, err := service.Resume(context.Background(), "session-t")
	if err != nil {
		t.Fatal(err)
	}
	if current.ID != "session-target" || current.Messages.SystemPrompt != "current-system" || current.Messages.History[1].ToolUses[0].ToolUseID != "call-1" {
		t.Fatalf("current = %#v", current)
	}
	if _, err := service.Resume(context.Background(), "missing"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("error = %v", err)
	}
	if service.Current().ID != "session-target" || service.Current().ID == before {
		t.Fatal("failed resume changed current session")
	}
}

func TestServiceRejectsAmbiguousPrefixAndCurrentDelete(t *testing.T) {
	store := newMemoryStore()
	for _, id := range []string{"session-abc1", "session-abc2"} {
		store.metadata[id] = contextmanager.SessionMetadata{ID: id, Workspace: "/repo", UpdatedAt: time.Now()}
	}
	service, _ := NewService(store, "/repo", "system")
	if _, err := service.Resume(context.Background(), "session-abc"); !errors.Is(err, ErrAmbiguousSessionID) {
		t.Fatalf("error = %v", err)
	}
	current := service.Current()
	store.metadata[current.ID] = contextmanager.SessionMetadata{ID: current.ID, Workspace: "/repo"}
	current.Persisted = true
	service.current = &current
	if err := service.Delete(context.Background(), current.ID); !errors.Is(err, ErrCurrentSessionDelete) {
		t.Fatalf("error = %v", err)
	}
}

func TestServiceRenameRollsBackOnStoreFailure(t *testing.T) {
	store := newMemoryStore()
	service, _ := NewService(store, "/repo", "system")
	if err := service.AddUserMessage(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	old := service.Current().Title
	store.renameErr = errors.New("disk full")
	if err := service.Rename(context.Background(), "new title"); err == nil {
		t.Fatal("expected error")
	}
	if service.Current().Title != old {
		t.Fatalf("title changed to %q", service.Current().Title)
	}
}
