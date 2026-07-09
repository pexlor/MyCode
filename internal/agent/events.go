package agent

type AgentEvent interface{ agentEvent() }

type TextEvent struct {
	Text string
}

type ThinkingEvent struct {
	Text string
}

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

type ToolResultEvent struct {
	ToolUseID string
	ToolName  string
	Content   string
	IsError   bool
}

type DoneEvent struct {
	StopReason string
}

type ErrorEvent struct {
	Err error
}

func (TextEvent) agentEvent()             {}
func (ThinkingEvent) agentEvent()         {}
func (ToolCallStartEvent) agentEvent()    {}
func (ToolCallDeltaEvent) agentEvent()    {}
func (ToolCallCompleteEvent) agentEvent() {}
func (ToolResultEvent) agentEvent()       {}
func (DoneEvent) agentEvent()             {}
func (ErrorEvent) agentEvent()            {}
