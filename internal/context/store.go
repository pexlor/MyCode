package contextmanager

import (
	"context"
	"errors"
	"io"
)

var (
	ErrInvalidIdentifier      = errors.New("invalid context identifier")
	ErrSummaryVersionConflict = errors.New("summary version conflict")
	ErrArtifactHashMismatch   = errors.New("tool artifact hash mismatch")
)

type ConversationStore interface {
	AppendMessage(context.Context, StoredMessage) error
	ListMessages(context.Context, string) ([]StoredMessage, error)
	ListMessagesAfter(context.Context, string, string) ([]StoredMessage, error)
	SaveToolArtifact(context.Context, ToolArtifact, io.Reader) error
	LoadToolArtifact(context.Context, string, string) (ToolArtifact, io.ReadCloser, error)
	ActiveSummary(context.Context, string) (*SummarySnapshot, error)
	CommitSummary(context.Context, SummarySnapshot, int) error
}
