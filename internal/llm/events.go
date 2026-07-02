package llm

// 流式事件

type StreamEvent interface {
	streamEvent()
}

// 普通 token 流
type TextStream struct {
	Text string
}

// 思考 token 流
type ThinkingStream struct {
	Text string
}

type ThinkingComplete struct {
	Thinking  string
	Signature string
}

type ToolCallStart struct {
	ToolName, ToolID string
}

type ToolCallStream struct {
	Text string
}

type ToolCallComplete struct {
	ToolID    string
	ToolName  string
	Arguments map[string]any
}

type UsageInfo struct {
	InputTokens         int
	OutputTokens        int
	CacheReadTokens     int
	CacheCreationTokens int
}

type StreamEnd struct {
	StopReason string
	Usage      UsageInfo
}

func (TextStream) streamEvent() {}

func (ThinkingStream) streamEvent()   {}
func (ThinkingComplete) streamEvent() {}

func (ToolCallStart) streamEvent()    {}
func (ToolCallStream) streamEvent()   {}
func (ToolCallComplete) streamEvent() {}

func (StreamEnd) streamEvent() {}
