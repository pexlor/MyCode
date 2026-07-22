package contextmanager

import (
	"context"
	"errors"
	"io"
)

var (
	// ErrInvalidIdentifier 防止 Session ID 或 Artifact ID 被用于路径穿越。
	ErrInvalidIdentifier = errors.New("invalid context identifier")
	// ErrSummaryVersionConflict 表示提交摘要时 active version 已被其他写入推进。
	ErrSummaryVersionConflict = errors.New("summary version conflict")
	// ErrArtifactHashMismatch 表示归档正文与元数据中的 SHA256 不一致。
	ErrArtifactHashMismatch = errors.New("tool artifact hash mismatch")
)

// ConversationStore 定义上下文管理所需的最小持久化能力。
// 实现必须保留完整消息，并保证 CommitSummary 不会让覆盖游标先于摘要正文生效。
type ConversationStore interface {
	AppendMessage(context.Context, StoredMessage) error
	ListMessages(context.Context, string) ([]StoredMessage, error)
	ListMessagesAfter(context.Context, string, string) ([]StoredMessage, error)
	SaveToolArtifact(context.Context, ToolArtifact, io.Reader) error
	LoadToolArtifact(context.Context, string, string) (ToolArtifact, io.ReadCloser, error)
	ActiveSummary(context.Context, string) (*SummarySnapshot, error)
	CommitSummary(context.Context, SummarySnapshot, int) error
}
