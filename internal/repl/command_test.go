package repl

import (
	contextmanager "MyCode/internal/context"
	"MyCode/internal/session"
	"bufio"
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCommandRegistryDistinguishesTextAndUnknownCommands(t *testing.T) {
	registry := NewCommandRegistry()
	if err := registry.Register(Command{Name: "ping", Usage: "/ping", Description: "ping", Run: func(context.Context, *CommandContext, string) CommandResult { return CommandResult{Handled: true} }}); err != nil {
		t.Fatal(err)
	}
	if got := registry.Execute(context.Background(), nil, "hello /ping"); got.Handled {
		t.Fatal("ordinary text was handled")
	}
	if got := registry.Execute(context.Background(), nil, "/missing"); !got.Handled || got.Err == nil {
		t.Fatalf("result = %#v", got)
	}
	if got := registry.Execute(context.Background(), nil, "/PING argument text"); !got.Handled || got.Err != nil {
		t.Fatalf("result = %#v", got)
	}
	if err := registry.Register(Command{Name: "ping", Run: func(context.Context, *CommandContext, string) CommandResult { return CommandResult{} }}); err == nil {
		t.Fatal("duplicate registration succeeded")
	}
}

func TestCommandHelpIsGeneratedFromRegistry(t *testing.T) {
	registry := NewCommandRegistry()
	for _, command := range []Command{{Name: "one", Usage: "/one", Description: "first"}, {Name: "two", Usage: "/two <id>", Description: "second"}} {
		command.Run = func(context.Context, *CommandContext, string) CommandResult { return CommandResult{Handled: true} }
		if err := registry.Register(command); err != nil {
			t.Fatal(err)
		}
	}
	var output bytes.Buffer
	commandContext := &CommandContext{Out: &output, Registry: registry}
	registerHelp(t, registry)
	result := registry.Execute(context.Background(), commandContext, "/help")
	if result.Err != nil || !strings.Contains(output.String(), "/one") || !strings.Contains(output.String(), "/two <id>") {
		t.Fatalf("output = %q, result = %#v", output.String(), result)
	}
	output.Reset()
	result = registry.Execute(context.Background(), commandContext, "/help /two")
	if result.Err != nil || !strings.Contains(output.String(), "second") {
		t.Fatalf("output = %q, result = %#v", output.String(), result)
	}
}

func TestDeleteCommandRequiresConfirmation(t *testing.T) {
	store := newCommandStore()
	store.metadata["session-delete"] = contextmanager.SessionMetadata{ID: "session-delete", Title: "old", Workspace: "/repo"}
	service, _ := session.NewService(store, "/repo", "system")
	registry, err := NewDefaultCommandRegistry()
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	commandContext := &CommandContext{Sessions: service, In: bufio.NewReader(strings.NewReader("n\n")), Out: &output, Registry: registry}
	result := registry.Execute(context.Background(), commandContext, "/delete session-del")
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if _, ok := store.metadata["session-delete"]; !ok {
		t.Fatal("session deleted after rejection")
	}
	commandContext.In = bufio.NewReader(strings.NewReader("yes\n"))
	result = registry.Execute(context.Background(), commandContext, "/delete session-del")
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if _, ok := store.metadata["session-delete"]; ok {
		t.Fatal("session was not deleted")
	}
}

func TestNewRenameCurrentAndExitCommands(t *testing.T) {
	service, _ := session.NewService(newCommandStore(), "/repo", "system")
	registry, _ := NewDefaultCommandRegistry()
	var output bytes.Buffer
	commandContext := &CommandContext{Sessions: service, In: bufio.NewReader(strings.NewReader("")), Out: &output, Registry: registry}
	if result := registry.Execute(context.Background(), commandContext, "/new named session"); result.Err != nil || result.Quit {
		t.Fatalf("result = %#v", result)
	}
	if service.Current().Title != "named session" {
		t.Fatalf("title = %q", service.Current().Title)
	}
	if result := registry.Execute(context.Background(), commandContext, "/rename renamed"); result.Err != nil {
		t.Fatal(result.Err)
	}
	if result := registry.Execute(context.Background(), commandContext, "/current"); result.Err != nil || !strings.Contains(output.String(), "renamed") {
		t.Fatalf("output = %q", output.String())
	}
	if result := registry.Execute(context.Background(), commandContext, "/exit"); !result.Quit {
		t.Fatalf("result = %#v", result)
	}
}

func TestShortIDOmitsFixedSessionPrefix(t *testing.T) {
	if got := shortID("session-abcdef123456"); got != "abcdef12" {
		t.Fatalf("short id = %q", got)
	}
}

func registerHelp(t *testing.T, registry *CommandRegistry) {
	t.Helper()
	if err := registry.Register(helpCommand()); err != nil {
		t.Fatal(err)
	}
}

type commandStore struct {
	metadata map[string]contextmanager.SessionMetadata
	messages map[string][]contextmanager.StoredMessage
}

func newCommandStore() *commandStore {
	return &commandStore{metadata: map[string]contextmanager.SessionMetadata{}, messages: map[string][]contextmanager.StoredMessage{}}
}
func (s *commandStore) CreateSession(_ context.Context, item contextmanager.SessionMetadata) error {
	s.metadata[item.ID] = item
	return nil
}
func (s *commandStore) GetSession(_ context.Context, id string) (contextmanager.SessionMetadata, error) {
	item, ok := s.metadata[id]
	if !ok {
		return contextmanager.SessionMetadata{}, contextmanager.ErrSessionNotFound
	}
	return item, nil
}
func (s *commandStore) ListSessions(_ context.Context, workspace string, _ int) ([]contextmanager.SessionMetadata, error) {
	var result []contextmanager.SessionMetadata
	for _, item := range s.metadata {
		if item.Workspace == workspace {
			result = append(result, item)
		}
	}
	return result, nil
}
func (s *commandStore) RenameSession(_ context.Context, id, title string) error {
	item, ok := s.metadata[id]
	if !ok {
		return errors.New("missing")
	}
	item.Title = title
	s.metadata[id] = item
	return nil
}
func (s *commandStore) DeleteSession(_ context.Context, id string) error {
	delete(s.metadata, id)
	return nil
}
func (s *commandStore) ListMessages(_ context.Context, id string) ([]contextmanager.StoredMessage, error) {
	return s.messages[id], nil
}
