package session

import (
	contextmanager "MyCode/internal/context"
	"MyCode/internal/message"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	DefaultTitle   = "未命名会话"
	MaxTitleRunes  = 80
	AutoTitleRunes = 40
)

var (
	ErrSessionNotFound      = errors.New("session not found")
	ErrAmbiguousSessionID   = errors.New("ambiguous session id")
	ErrCurrentSessionDelete = errors.New("cannot delete current session")
	ErrInvalidSessionTitle  = errors.New("invalid session title")
)

type Store interface {
	CreateSession(context.Context, contextmanager.SessionMetadata) error
	GetSession(context.Context, string) (contextmanager.SessionMetadata, error)
	ListSessions(context.Context, string, int) ([]contextmanager.SessionMetadata, error)
	RenameSession(context.Context, string, string) error
	DeleteSession(context.Context, string) error
	ListMessages(context.Context, string) ([]contextmanager.StoredMessage, error)
}

type CurrentSession struct {
	ID            string
	Title         string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	Persisted     bool
	ExplicitTitle bool
	Messages      *message.MessageManager
}

type Service struct {
	store        Store
	workspace    string
	systemPrompt string
	current      *CurrentSession
}

func NewService(store Store, workspace, systemPrompt string) (*Service, error) {
	if store == nil || strings.TrimSpace(workspace) == "" {
		return nil, errors.New("session store and workspace are required")
	}
	s := &Service{store: store, workspace: workspace, systemPrompt: systemPrompt}
	if _, err := s.New(context.Background(), ""); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Service) New(_ context.Context, title string) (*CurrentSession, error) {
	title, explicit, err := normalizeOptionalTitle(title)
	if err != nil {
		return nil, err
	}
	id, err := newSessionID()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	s.current = &CurrentSession{ID: id, Title: title, CreatedAt: now, UpdatedAt: now, ExplicitTitle: explicit, Messages: &message.MessageManager{SystemPrompt: s.systemPrompt}}
	current := s.Current()
	return &current, nil
}

func (s *Service) Current() CurrentSession {
	if s.current == nil {
		return CurrentSession{}
	}
	return *s.current
}

func (s *Service) AddUserMessage(ctx context.Context, content string) error {
	if s.current == nil {
		return errors.New("current session is not initialized")
	}
	if !s.current.Persisted {
		if !s.current.ExplicitTitle {
			s.current.Title = autoTitle(content)
		}
		metadata := contextmanager.SessionMetadata{ID: s.current.ID, Title: s.current.Title, Workspace: s.workspace, CreatedAt: s.current.CreatedAt, UpdatedAt: time.Now()}
		if err := s.store.CreateSession(ctx, metadata); err != nil {
			return fmt.Errorf("create session %s: %w", s.current.ID, err)
		}
		s.current.Persisted = true
	}
	s.current.Messages.AddText(content)
	s.current.UpdatedAt = time.Now()
	return nil
}

func (s *Service) List(ctx context.Context, limit int) ([]contextmanager.SessionMetadata, error) {
	items, err := s.store.ListSessions(ctx, s.workspace, limit)
	if err != nil {
		return nil, err
	}
	if s.current != nil && !s.current.Persisted {
		current := contextmanager.SessionMetadata{ID: s.current.ID, Title: s.current.Title, Workspace: s.workspace, CreatedAt: s.current.CreatedAt, UpdatedAt: s.current.UpdatedAt, MessageCount: len(s.current.Messages.History)}
		items = append([]contextmanager.SessionMetadata{current}, items...)
	}
	return items, nil
}

func (s *Service) Resume(ctx context.Context, idOrPrefix string) (*CurrentSession, error) {
	metadata, err := s.resolve(ctx, idOrPrefix)
	if err != nil {
		return nil, err
	}
	if s.current != nil && metadata.ID == s.current.ID {
		current := s.Current()
		return &current, nil
	}
	stored, err := s.store.ListMessages(ctx, metadata.ID)
	if err != nil {
		return nil, fmt.Errorf("load session %s: %w", metadata.ID, err)
	}
	manager := &message.MessageManager{SystemPrompt: s.systemPrompt, History: restoreMessages(stored)}
	next := &CurrentSession{ID: metadata.ID, Title: metadata.Title, CreatedAt: metadata.CreatedAt, UpdatedAt: metadata.UpdatedAt, Persisted: true, ExplicitTitle: metadata.Title != "" && metadata.Title != DefaultTitle, Messages: manager}
	s.current = next
	current := s.Current()
	return &current, nil
}

func (s *Service) Delete(ctx context.Context, idOrPrefix string) error {
	metadata, err := s.resolve(ctx, idOrPrefix)
	if err != nil {
		return err
	}
	if s.current != nil && metadata.ID == s.current.ID {
		return ErrCurrentSessionDelete
	}
	return s.store.DeleteSession(ctx, metadata.ID)
}

func (s *Service) Rename(ctx context.Context, title string) error {
	title = strings.TrimSpace(title)
	if title == "" || utf8.RuneCountInString(title) > MaxTitleRunes {
		return ErrInvalidSessionTitle
	}
	if s.current.Persisted {
		if err := s.store.RenameSession(ctx, s.current.ID, title); err != nil {
			return fmt.Errorf("rename session %s: %w", s.current.ID, err)
		}
	}
	s.current.Title = title
	s.current.ExplicitTitle = true
	s.current.UpdatedAt = time.Now()
	return nil
}

func (s *Service) resolve(ctx context.Context, prefix string) (contextmanager.SessionMetadata, error) {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return contextmanager.SessionMetadata{}, ErrSessionNotFound
	}
	if exact, err := s.store.GetSession(ctx, prefix); err == nil {
		if exact.Workspace != s.workspace {
			return contextmanager.SessionMetadata{}, ErrSessionNotFound
		}
		return exact, nil
	} else if !errors.Is(err, contextmanager.ErrSessionNotFound) && !errors.Is(err, contextmanager.ErrInvalidIdentifier) {
		return contextmanager.SessionMetadata{}, err
	}
	items, err := s.store.ListSessions(ctx, s.workspace, 0)
	if err != nil {
		return contextmanager.SessionMetadata{}, err
	}
	var matches []contextmanager.SessionMetadata
	for _, item := range items {
		if strings.HasPrefix(item.ID, prefix) {
			matches = append(matches, item)
		}
	}
	if len(matches) == 0 {
		return contextmanager.SessionMetadata{}, ErrSessionNotFound
	}
	if len(matches) > 1 {
		return contextmanager.SessionMetadata{}, ErrAmbiguousSessionID
	}
	return matches[0], nil
}

func restoreMessages(stored []contextmanager.StoredMessage) []message.Message {
	result := make([]message.Message, 0, len(stored))
	for _, item := range stored {
		converted := message.Message{Role: item.Role, Content: item.Content}
		for _, use := range item.ToolUses {
			converted.ToolUses = append(converted.ToolUses, message.ToolUseBlock{ToolUseID: use.ToolUseID, ToolName: use.ToolName, Arguments: use.Arguments})
		}
		for _, toolResult := range item.ToolResults {
			converted.ToolResults = append(converted.ToolResults, message.ToolResultBlock{ToolUseID: toolResult.ToolUseID, Content: toolResult.Content, IsError: toolResult.IsError})
		}
		result = append(result, converted)
	}
	return result
}

func normalizeOptionalTitle(title string) (string, bool, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return DefaultTitle, false, nil
	}
	if utf8.RuneCountInString(title) > MaxTitleRunes {
		return "", false, ErrInvalidSessionTitle
	}
	return title, true, nil
}

func autoTitle(content string) string {
	line := content
	if index := strings.IndexByte(line, '\n'); index >= 0 {
		line = line[:index]
	}
	line = strings.Join(strings.Fields(line), " ")
	if line == "" {
		return DefaultTitle
	}
	runes := []rune(line)
	if len(runes) > AutoTitleRunes {
		runes = runes[:AutoTitleRunes]
	}
	return string(runes)
}

func newSessionID() (string, error) {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	return "session-" + hex.EncodeToString(bytes), nil
}
