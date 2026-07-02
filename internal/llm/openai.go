package llm

import (
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/shared"
)

const openaiStreamIdleTimeout = 5 * time.Minute

type OpeaAiClient struct {
	client       openai.Client
	modelParm    *ModelParm
	systemPrompt string
}

func newBaiLianClient(parm *ModelParm, systemPrompt string) (*OpeaAiClient, error) {
	client := openai.NewClient(
		option.WithAPIKey(parm.APIKey),
		option.WithBaseURL(parm.BaseURL),
	)
	return &OpeaAiClient{
		client:       client,
		modelParm:    parm,
		systemPrompt: systemPrompt,
	}, nil
}

func (c *OpeaAiClient) stream(req *StreamRequest) (<-chan StreamEvent, <-chan error) {
	eventsChan := make(chan StreamEvent, 128)
	errsChan := make(chan error, 128)

	// 构建消息
	var messages []openai.ChatCompletionMessageParamUnion

	// 添加工具信息
	var tools []openai.ChatCompletionToolUnionParam

	for _, tool := range req.Tools {
		tools = append(tools, openai.ChatCompletionToolUnionParam{
			OfFunction: &openai.ChatCompletionFunctionToolParam{
				Function: shared.FunctionDefinitionParam{
					Name:        tool.name,
					Description: param.NewOpt(tool.description),
					Parameters:  tool.parameters,
					Strict:      param.NewOpt(false),
				},
			},
		})
	}

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
		if len(tools) > 0 {
			reqParams.Tools = tools
		}
		// 发起请求
		stream := c.client.Chat.Completions.NewStreaming(ctx, reqParams)
		defer stream.Close()

		// Track tool calls being assembled across multiple chunks.
		// The Chat Completions API sends tool call information incrementally:
		// the first chunk for a given index carries the ID and function name,
		// subsequent chunks carry argument fragments.
		type toolCallAccum struct {
			id       string
			name     string
			argsJSON string
		}
		toolCalls := make(map[int64]*toolCallAccum)

		// sse 无响应超时时间
		idle := time.NewTimer(openaiStreamIdleTimeout)
		defer idle.Stop()

		for stream.Next() {
			idle.Reset(openaiStreamIdleTimeout)
			chunk := stream.Current()

			// 不知道在干嘛？
			if chunk.JSON.Usage.Valid() && chunk.Usage.PromptTokens != 0 {
				eventsChan <- StreamEnd{ /*...*/ }
				continue
			}

			delta := chunk.Choices[0].Delta
			if delta.Content != "" {
				eventsChan <- TextStream{Text: delta.Content}
			}

			// todo: add thinking

			// 2. tool calls
			for _, tc := range delta.ToolCalls {

				acc, ok := toolCalls[tc.Index]
				if !ok {
					acc = &toolCallAccum{}
					toolCalls[tc.Index] = acc
				}

				if tc.Function.Name != "" {
					acc.name = tc.Function.Name
					eventsChan <- ToolCallStart{ToolName: acc.name}
				}

				if tc.Function.Arguments != "" {
					acc.argsJSON += tc.Function.Arguments
					eventsChan <- ToolCallStream{Text: tc.Function.Arguments}
				}
			}

			// 3. finish
			if chunk.Choices[0].FinishReason == "stop" {
				eventsChan <- StreamEnd{}
			}
		}

		if err := stream.Err(); err != nil {
			errsChan <- err
		}
	}()
	return eventsChan, errsChan
}
