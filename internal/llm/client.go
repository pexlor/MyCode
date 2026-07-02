package llm

import (
	"context"
	"fmt"
)

type Message struct {
	role    string
	content string
}

type ToolSchema struct {
	name        string
	description string
	parameters  map[string]any
}

type StreamRequest struct {
	Context  context.Context
	Messages []Message
	Tools    []ToolSchema
}

type LLMClient interface {
	stream(req *StreamRequest) (<-chan StreamEvent, <-chan error)
}

// 对话模型参数
type ModelParm struct {
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

func NewClient(parm *ModelParm, systemPrompt string) (*LLMClient, error) {
	switch parm.Provider {
	default:
		return nil, fmt.Errorf("unknown model provider: %s", parm.Provider)
	}
}
