package llm

import (
	"encoding/json"
	"fmt"
	"sort"
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
	if parm == nil {
		return nil, fmt.Errorf("%w: model parameters cannot be nil", ErrInvalidConfig)
	}
	if parm.APIKey == "" || parm.BaseURL == "" {
		return nil, fmt.Errorf("%w: APIKey and BaseURL are required", ErrInvalidConfig)
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
	if req == nil || req.Context == nil {
		close(eventsChan)
		errsChan <- fmt.Errorf("%w: request and context are required", ErrInvalidRequest)
		close(errsChan)
		return eventsChan, errsChan
	}

	// 构建消息
	messages := buildChatCompletionMessages(req)
	go func() {
		defer close(eventsChan)
		defer close(errsChan)

		ctx := req.Context

		// 构建工具
		var tools []openai.ChatCompletionToolUnionParam
		for _, t := range req.Tools {
			tools = append(tools, openai.ChatCompletionFunctionTool(openai.FunctionDefinitionParam{
				Name:        t.Name,
				Description: openai.String(t.Description),
				Parameters:  openai.FunctionParameters(t.Parameters),
			}))
		}

		// 构建请求参数
		reqParams := openai.ChatCompletionNewParams{
			Model:    c.modelParm.ModelName,
			Messages: messages,
			Tools:    tools,
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

		type toolCallAccum struct {
			id        string
			name      string
			arguments string
		}
		toolCalls := make(map[int64]*toolCallAccum)

		// 事件处理
		for {
			select {
			case <-ctx.Done():
				errsChan <- ctx.Err()
				return
			case <-idle.C:
				errsChan <- ErrStreamIdleTimeout
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
				// text content delta
				if delta.Content != "" {
					eventsChan <- TextStream{Text: delta.Content}
				}

				// Accumulate tool call deltas. Arguments may span multiple chunks.
				for _, tc := range delta.ToolCalls {
					call := toolCalls[tc.Index]
					if call == nil {
						call = &toolCallAccum{}
						toolCalls[tc.Index] = call
					}
					if tc.ID != "" {
						call.id = tc.ID
					}
					if tc.Function.Name != "" {
						call.name = tc.Function.Name
						eventsChan <- ToolCallStart{ToolName: call.name, ToolID: call.id}
					}
					if tc.Function.Arguments != "" {
						call.arguments += tc.Function.Arguments
						eventsChan <- ToolCallStream{ToolID: call.id, Text: tc.Function.Arguments}
					}
				}

				if choice.FinishReason == "tool_calls" {
					indices := make([]int64, 0, len(toolCalls))
					for index := range toolCalls {
						indices = append(indices, index)
					}
					sort.Slice(indices, func(i, j int) bool { return indices[i] < indices[j] })
					for _, index := range indices {
						call := toolCalls[index]
						arguments := make(map[string]any)
						if call.arguments != "" {
							if err := json.Unmarshal([]byte(call.arguments), &arguments); err != nil {
								errsChan <- fmt.Errorf("decode arguments for tool %q: %w", call.name, err)
								return
							}
						}
						eventsChan <- ToolCallComplete{
							ToolID:    call.id,
							ToolName:  call.name,
							Arguments: arguments,
						}
					}
					eventsChan <- StreamEnd{StopReason: choice.FinishReason}
				} else if choice.FinishReason == "stop" {
					eventsChan <- StreamEnd{StopReason: choice.FinishReason}
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
		// todo 添加tool
		switch m.Role {
		case "assistant":
			message := openai.AssistantMessage(m.Content)
			for _, call := range m.ToolCalls {
				message.OfAssistant.ToolCalls = append(
					message.OfAssistant.ToolCalls,
					openai.ChatCompletionMessageToolCallUnionParam{
						OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
							ID: call.ID,
							Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
								Name:      call.Name,
								Arguments: call.Arguments,
							},
						},
					},
				)
			}
			result = append(result, message)
		case "tool":
			result = append(result, openai.ToolMessage(m.Content, m.ToolCallID))
		default:
			result = append(result, openai.UserMessage(m.Content))
		}
	}
	return result
}
