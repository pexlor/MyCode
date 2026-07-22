package contextmanager

import "time"

type TurnStatus string

const (
	TurnOpen     TurnStatus = "open"
	TurnComplete TurnStatus = "complete"
)

type ResultState string

const (
	ResultFull      ResultState = "full"
	ResultReference ResultState = "reference"
	ResultDropped   ResultState = "dropped"
)

type StoredToolUse struct {
	ToolUseID string         `json:"tool_use_id"`
	ToolName  string         `json:"tool_name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type StoredToolResult struct {
	ToolUseID  string      `json:"tool_use_id"`
	ToolName   string      `json:"tool_name"`
	Content    string      `json:"content"`
	IsError    bool        `json:"is_error"`
	ArtifactID string      `json:"artifact_id,omitempty"`
	State      ResultState `json:"state,omitempty"`
}

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

type sessionManifest struct {
	FormatVersion           int       `json:"format_version"`
	SessionID               string    `json:"session_id"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
	ActiveSummaryVersion    int       `json:"active_summary_version"`
	CoveredThroughMessageID string    `json:"covered_through_message_id,omitempty"`
	CoveredThroughTurnID    string    `json:"covered_through_turn_id,omitempty"`
}
