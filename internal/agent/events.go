package agent

import "MyCode/internal/llm"

type AgentEvent interface{ agentEvent() }

type TextEvent struct {
	Text string
}

type ThinkingEvent struct {
	Text string
}

// ThinkingStartEvent indicates that the agent has started a model request.
// It is emitted even when the provider does not expose reasoning tokens, so a
// UI can still show that the conversation is making progress.
type ThinkingStartEvent struct{}

type ToolCallStartEvent struct {
	ToolUseID string
	ToolName  string
}

type ToolCallDeltaEvent struct {
	ToolUseID string
	Text      string
}

type ToolCallCompleteEvent struct {
	ToolUseID string
	ToolName  string
	Arguments map[string]any
}

// ToolExecutionStartEvent is emitted immediately before a requested tool is
// executed. ToolCallStartEvent only means that the model started describing a
// call; this event represents the actual side effecting operation.
type ToolExecutionStartEvent struct {
	ToolUseID string
	ToolName  string
}

type ToolResultEvent struct {
	ToolUseID string
	ToolName  string
	Content   string
	IsError   bool
}

type DoneEvent struct {
	StopReason string
	Usage      llm.UsageInfo
}

type ErrorEvent struct {
	Err error
}

func (TextEvent) agentEvent()               {}
func (ThinkingEvent) agentEvent()           {}
func (ThinkingStartEvent) agentEvent()      {}
func (ToolCallStartEvent) agentEvent()      {}
func (ToolCallDeltaEvent) agentEvent()      {}
func (ToolCallCompleteEvent) agentEvent()   {}
func (ToolExecutionStartEvent) agentEvent() {}
func (ToolResultEvent) agentEvent()         {}
func (DoneEvent) agentEvent()               {}
func (ErrorEvent) agentEvent()              {}
