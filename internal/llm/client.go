package llm

import (
	"context"
	"fmt"
)

type Message struct {
	Role       string
	Content    string
	ToolCallID string
	ToolCalls  []ToolCall
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

type ToolSchema struct {
	Name        string
	Description string
	Parameters  map[string]any
}

type StreamRequest struct {
	Context      context.Context
	SystemPrompt string
	Messages     []Message
	Tools        []ToolSchema
}

type LLMClient interface {
	Stream(req *StreamRequest) (<-chan StreamEvent, <-chan error)
}

// 对话模型参数
type ModelParm struct {
	Protocol  string
	ModelName string
	Provider  string

	BaseURL string
	APIKey  string

	TopK float64
	TopP float64
	Temp float64

	Tinking bool

	MaxToken      int64
	ContextWindow int64
}

func NewClient(parm *ModelParm) (LLMClient, error) {
	switch parm.Protocol {
	case "openai-compat":
		return newOpenAiCompatClient(parm)
	default:
		return nil, fmt.Errorf("unknown model protocol: %s", parm.Protocol)
	}
}
