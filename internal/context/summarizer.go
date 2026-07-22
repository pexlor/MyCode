package contextmanager

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"MyCode/internal/llm"
	"MyCode/internal/message"
)

type LLMSummarizer struct {
	Client llm.LLMClient
}

// Summarize 通过独立 LLMClient 生成结构化工作记忆。
// 请求显式传入空 Tools，并拒绝任何工具调用事件，确保摘要过程不会产生副作用。
func (s LLMSummarizer) Summarize(ctx context.Context, request SummarizeRequest) (SummarizeResponse, error) {
	if s.Client == nil {
		return SummarizeResponse{}, errors.New("summary client is required")
	}
	payload, err := json.Marshal(struct {
		PreviousSummary string          `json:"previous_summary"`
		Messages        []StoredMessage `json:"new_messages"`
	}{PreviousSummary: request.PreviousSummary, Messages: request.Messages})
	if err != nil {
		return SummarizeResponse{}, err
	}
	events, errs := s.Client.Stream(&llm.StreamRequest{
		Context: ctx,
		SystemPrompt: "You are a context compressor. Produce only factual structured task memory. " +
			"Do not call tools, answer the user, invent facts, or include hidden reasoning. " +
			"Preserve goals, constraints, decisions, changed files, command results, unresolved issues, artifact references, and next steps.",
		Messages: []message.Message{{Role: message.USER, Content: string(payload)}},
		Tools:    nil,
	})
	var builder strings.Builder
	// 同时消费事件和错误通道，直到两者关闭，避免流式客户端因无人读取而阻塞。
	for events != nil || errs != nil {
		select {
		case <-ctx.Done():
			return SummarizeResponse{}, ctx.Err()
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err != nil {
				return SummarizeResponse{}, err
			}
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			switch item := event.(type) {
			case llm.TextStream:
				builder.WriteString(item.Text)
			case llm.ToolCallStart, llm.ToolCallStream, llm.ToolCallComplete:
				return SummarizeResponse{}, errors.New("summary model attempted a tool call")
			}
		}
	}
	content := strings.TrimSpace(builder.String())
	if content == "" {
		return SummarizeResponse{}, errors.New("summary model returned empty content")
	}
	return SummarizeResponse{Content: content}, nil
}
