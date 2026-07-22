package contextmanager

import "time"

type TurnStatus string

const (
	// TurnOpen 表示本轮对话仍可能继续产生工具调用或最终回复，不能参与摘要压缩。
	TurnOpen TurnStatus = "open"
	// TurnComplete 表示本轮对话已经得到最终回复，可以安全地作为整体参与压缩。
	TurnComplete TurnStatus = "complete"
)

type ResultState string

const (
	// ResultFull 表示模型上下文中仍保留完整工具输出。
	ResultFull ResultState = "full"
	// ResultReference 表示完整输出已归档，上下文中只保留摘要和 Artifact 引用。
	ResultReference ResultState = "reference"
	// ResultDropped 表示该结果已从当前上下文视图移除，但原始 transcript 仍然保留。
	ResultDropped ResultState = "dropped"
)

// StoredToolUse 是写入 transcript 的工具调用事实。
// 它与面向模型的 message.ToolUseBlock 分离，避免压缩 ContextView 时破坏原始记录。
type StoredToolUse struct {
	ToolUseID string         `json:"tool_use_id"`
	ToolName  string         `json:"tool_name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// StoredToolResult 保存工具执行的原始结果及其上下文状态。
// ArtifactID 为空表示尚未归档；归档以后 Content 可以在 ContextView 中被替换为短引用。
type StoredToolResult struct {
	ToolUseID  string      `json:"tool_use_id"`
	ToolName   string      `json:"tool_name"`
	Content    string      `json:"content"`
	IsError    bool        `json:"is_error"`
	ArtifactID string      `json:"artifact_id,omitempty"`
	State      ResultState `json:"state,omitempty"`
}

// StoredMessage 是 ConversationStore 中的权威消息记录。
// ID 用于持久化游标，TurnID 用于保证摘要只发生在完整轮次边界。
type StoredMessage struct {
	ID          string             `json:"id"`
	SessionID   string             `json:"session_id"`
	TurnID      string             `json:"turn_id"`
	Iteration   int                `json:"iteration,omitempty"`
	Role        string             `json:"role"`
	Content     string             `json:"content,omitempty"`
	ToolUses    []StoredToolUse    `json:"tool_uses,omitempty"`
	ToolResults []StoredToolResult `json:"tool_results,omitempty"`
	CreatedAt   time.Time          `json:"created_at"`
	TurnStatus  TurnStatus         `json:"turn_status,omitempty"`
}

// ToolArtifact 描述一个已经从模型上下文卸载到磁盘的完整工具结果。
// ContentSHA256 用于读取时校验，避免模型依据损坏或被替换的归档继续推理。
type ToolArtifact struct {
	ID               string    `json:"id"`
	SessionID        string    `json:"session_id"`
	ToolUseID        string    `json:"tool_use_id"`
	ToolName         string    `json:"tool_name"`
	ArgumentsSummary string    `json:"arguments_summary,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	IsError          bool      `json:"is_error"`
	ByteSize         int64     `json:"byte_size"`
	TokenEstimate    int       `json:"token_estimate"`
	ContentSHA256    string    `json:"content_sha256"`
	StoragePath      string    `json:"storage_path"`
	Preview          string    `json:"preview,omitempty"`
}

// SummarySnapshot 是一次已提交的增量摘要及其压缩检查点。
// CoveredThroughMessageID 之前（含该消息）的原文不会再次进入 ContextView，
// 但仍永久保留在 transcript 中供审计和精确恢复。
type SummarySnapshot struct {
	Version                 int       `json:"version"`
	SessionID               string    `json:"session_id"`
	CoveredThroughMessageID string    `json:"covered_through_message_id,omitempty"`
	CoveredThroughTurnID    string    `json:"covered_through_turn_id,omitempty"`
	PreviousSummaryVersion  int       `json:"previous_summary_version,omitempty"`
	Content                 string    `json:"content"`
	TokenEstimate           int       `json:"token_estimate,omitempty"`
	ArtifactIDs             []string  `json:"artifact_ids,omitempty"`
	CreatedAt               time.Time `json:"created_at"`
}

// sessionManifest 只保存当前生效的摘要版本及其覆盖游标。
// summaries 目录中可能存在尚未激活的孤立文件，构建上下文时必须以 manifest 为准。
type sessionManifest struct {
	FormatVersion           int       `json:"format_version"`
	SessionID               string    `json:"session_id"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
	ActiveSummaryVersion    int       `json:"active_summary_version"`
	CoveredThroughMessageID string    `json:"covered_through_message_id,omitempty"`
	CoveredThroughTurnID    string    `json:"covered_through_turn_id,omitempty"`
}
