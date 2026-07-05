package llm

import (
	"errors"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
)

const openaiStreamIdleTimeout = 5 * time.Minute

type OpenAiCompatClient struct {
	client    openai.Client
	modelParm *ModelParm
}

func newOpenAiCompatClient(parm *ModelParm) (*OpenAiCompatClient, error) {
	if parm.APIKey == "" || parm.BaseURL == "" {
		return nil, errors.New("APIKey or BaseURL is empty")
	}
	client := openai.NewClient(
		option.WithAPIKey(parm.APIKey),
		option.WithBaseURL(parm.BaseURL),
	)
	return &OpenAiCompatClient{
		client:    client,
		modelParm: parm,
	}, nil
}

func (c *OpenAiCompatClient) Stream(req *StreamRequest) (<-chan StreamEvent, <-chan error) {
	eventsChan := make(chan StreamEvent, 128)
	errsChan := make(chan error, 128)

	// 构建消息
	messages := buildChatCompletionMessages(req)
	go func() {
		defer close(eventsChan)
		defer close(errsChan)

		ctx := req.Context

		// 构建请求参数
		reqParams := openai.ChatCompletionNewParams{
			Model:    c.modelParm.ModelName,
			Messages: messages,
			StreamOptions: openai.ChatCompletionStreamOptionsParam{
				IncludeUsage: param.NewOpt(true),
			},
		}
		// 发起请求
		stream := c.client.Chat.Completions.NewStreaming(ctx, reqParams)
		defer stream.Close()

		// sse 无响应超时时间
		idle := time.NewTimer(openaiStreamIdleTimeout)
		defer idle.Stop()

		// 接收stream
		type steamChunk struct {
			chunk    openai.ChatCompletionChunk
			finished bool
		}
		streamChan := make(chan steamChunk, 128)
		go func() {
			for stream.Next() {
				streamChan <- steamChunk{chunk: stream.Current(), finished: false}
			}
			streamChan <- steamChunk{finished: true}
		}()

		// 事件处理
		for {
			select {
			case <-ctx.Done():
				return
			case <-idle.C:
				return
			case onechunk := <-streamChan:
				if onechunk.finished {
					if err := stream.Err(); err != nil {
						errsChan <- err
					}
					return
				}
				chunk := onechunk.chunk
				if len(chunk.Choices) == 0 {
					continue
				}
				choice := chunk.Choices[0]
				delta := choice.Delta

				if delta.Content != "" {
					eventsChan <- TextStream{Text: delta.Content}
				}

				// finish
				if chunk.Choices[0].FinishReason == "stop" {
					eventsChan <- StreamEnd{}
				}
				idle.Reset(openaiStreamIdleTimeout)
			}
		}
	}()

	return eventsChan, errsChan
}

func buildChatCompletionMessages(req *StreamRequest) []openai.ChatCompletionMessageParamUnion {
	var result []openai.ChatCompletionMessageParamUnion

	if req.SystemPrompt != "" {
		result = append(result, openai.SystemMessage(req.SystemPrompt))
	}
	for _, m := range req.Messages {
		if m.Role == "assistant" {
			result = append(result, openai.AssistantMessage(m.Content))
		} else {
			// User messages
			result = append(result, openai.UserMessage(m.Content))
		}
	}
	return result
}
