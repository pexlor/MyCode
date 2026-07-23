package llm

import (
	"MyCode/internal/message"
	"MyCode/internal/tool"
	"context"
	"fmt"
)

type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

type StreamRequest struct {
	Context      context.Context
	SystemPrompt string
	Messages     []message.Message
	Tools        []*tool.ToolSchema
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
	if parm == nil {
		return nil, fmt.Errorf("%w: model parameters cannot be nil", ErrInvalidConfig)
	}
	switch parm.Protocol {
	case "openai-compat":
		return newOpenAiCompatClient(parm)
	case "anthropic":
		return newAnthropicClient(parm)
	default:
		return nil, fmt.Errorf("unknown model protocol: %s", parm.Protocol)
	}
}
