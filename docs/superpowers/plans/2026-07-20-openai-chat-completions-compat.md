# OpenAI Chat Completions 兼容协议实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 完善现有 OpenAI Chat Completions 流式客户端，使其可靠映射请求参数，并完整处理文本、usage、并行工具调用、结束原因、取消、超时和错误。

**架构：** 保留 `openai-go` SDK 作为 HTTP/SSE 传输层。`openai_compat.go` 负责配置校验、请求构建和流生命周期编排；新建 `openai_stream_parser.go`，用独立状态机把 `ChatCompletionChunk` 转换为内部 `StreamEvent`，从而让协议行为能脱离网络单独测试。

**技术栈：** Go 1.24、`github.com/openai/openai-go/v3` v3.41.0、标准库 `httptest`、Go `testing`。

---

## 文件结构

- 修改：`internal/llm/openai_compat.go`
  - 校验客户端配置。
  - 构建 Chat Completions 请求参数和历史消息。
  - 编排 SDK 流、context、空闲计时器、事件及错误 channel。
- 创建：`internal/llm/openai_stream_parser.go`
  - 保存单次 completion 的 usage、结束原因和工具调用累计状态。
  - 将单个 SDK chunk 转换为内部事件。
  - 在 SDK 流结束时完成协议校验并产生唯一的 `StreamEnd`。
- 创建：`internal/llm/openai_compat_test.go`
  - 覆盖请求构建、消息转换、流状态机和本地 SSE 集成路径。

## 任务 1：请求构建和配置校验

**文件：**

- 修改：`internal/llm/openai_compat.go:21-76`
- 创建：`internal/llm/openai_compat_test.go`

- [ ] **步骤 1：编写配置校验和请求参数映射的失败测试**

在 `internal/llm/openai_compat_test.go` 中加入：

```go
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"MyCode/internal/message"
	"MyCode/internal/tool"
)

func TestNewOpenAICompatClientRequiresModelName(t *testing.T) {
	_, err := newOpenAiCompatClient(&ModelParm{
		APIKey:  "test-key",
		BaseURL: "http://example.com/v1",
	})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got %v", err)
	}
}

func TestBuildOpenAIChatCompletionParamsMapsConfiguredFields(t *testing.T) {
	req := &StreamRequest{
		Context:      context.Background(),
		SystemPrompt: "system prompt",
		Messages: []message.Message{
			{Role: message.USER, Content: "hello"},
			{
				Role:    message.ASSISTANT,
				Content: "checking",
				ToolUses: []message.ToolUseBlock{{
					ToolUseID: "call-1",
					ToolName:  "lookup",
					Arguments: map[string]any{"city": "Beijing"},
				}},
			},
			{
				Role: message.TOOL,
				ToolResults: []message.ToolResultBlock{{
					ToolUseID: "call-1",
					Content:   "sunny",
				}},
			},
		},
		Tools: []*tool.ToolSchema{{
			Name:        "lookup",
			Description: "look up weather",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"city": map[string]any{"type": "string"},
				},
			},
		}},
	}
	parm := &ModelParm{
		ModelName: "test-model",
		Temp:      0.7,
		TopP:      0.9,
		TopK:      40,
		MaxToken:  1024,
	}

	params, err := buildOpenAIChatCompletionParams(req, parm)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}

	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "test-model" {
		t.Fatalf("unexpected model: %#v", body["model"])
	}
	if body["temperature"] != 0.7 || body["top_p"] != 0.9 {
		t.Fatalf("sampling parameters not mapped: %s", raw)
	}
	if body["max_completion_tokens"] != float64(1024) {
		t.Fatalf("max_completion_tokens not mapped: %s", raw)
	}
	if _, ok := body["top_k"]; ok {
		t.Fatalf("non-standard top_k must not be sent: %s", raw)
	}
	streamOptions := body["stream_options"].(map[string]any)
	if streamOptions["include_usage"] != true {
		t.Fatalf("include_usage not enabled: %s", raw)
	}
	if len(body["messages"].([]any)) != 4 {
		t.Fatalf("unexpected messages: %s", raw)
	}
	if len(body["tools"].([]any)) != 1 {
		t.Fatalf("unexpected tools: %s", raw)
	}
}

func TestBuildOpenAIChatCompletionParamsOmitsUnsetOptionalFields(t *testing.T) {
	params, err := buildOpenAIChatCompletionParams(
		&StreamRequest{Context: context.Background()},
		&ModelParm{ModelName: "test-model"},
	)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"temperature", "top_p", "max_completion_tokens", "tools"} {
		if _, ok := body[key]; ok {
			t.Fatalf("%s must be omitted: %s", key, raw)
		}
	}
}

func TestBuildOpenAIChatCompletionParamsRejectsUnserializableToolArguments(t *testing.T) {
	_, err := buildOpenAIChatCompletionParams(&StreamRequest{
		Context: context.Background(),
		Messages: []message.Message{{
			Role: message.ASSISTANT,
			ToolUses: []message.ToolUseBlock{{
				ToolUseID: "call-1",
				ToolName:  "broken",
				Arguments: map[string]any{"bad": make(chan int)},
			}},
		}},
	}, &ModelParm{ModelName: "test-model"})
	if err == nil {
		t.Fatal("expected JSON marshal error")
	}
}
```

- [ ] **步骤 2：运行测试并确认因缺少请求构建器和模型名校验而失败**

运行：

```bash
go test ./internal/llm -run 'Test(NewOpenAICompatClientRequiresModelName|BuildOpenAIChatCompletionParams)' -count=1
```

预期：FAIL，编译错误包含 `undefined: buildOpenAIChatCompletionParams`；实现函数后但补模型名校验前，`TestNewOpenAICompatClientRequiresModelName` 仍应失败。

- [ ] **步骤 3：实现最小请求构建器和配置校验**

在 `internal/llm/openai_compat.go` 中：

```go
func newOpenAiCompatClient(parm *ModelParm) (*OpenAiCompatClient, error) {
	if parm == nil {
		return nil, fmt.Errorf("%w: model parameters cannot be nil", ErrInvalidConfig)
	}
	if parm.APIKey == "" || parm.BaseURL == "" || parm.ModelName == "" {
		return nil, fmt.Errorf("%w: APIKey, BaseURL and ModelName are required", ErrInvalidConfig)
	}
	client := openai.NewClient(
		option.WithAPIKey(parm.APIKey),
		option.WithBaseURL(parm.BaseURL),
	)
	return &OpenAiCompatClient{client: client, modelParm: parm}, nil
}

func buildOpenAIChatCompletionParams(req *StreamRequest, parm *ModelParm) (openai.ChatCompletionNewParams, error) {
	messages, err := buildChatCompletionMessages(req)
	if err != nil {
		return openai.ChatCompletionNewParams{}, err
	}
	params := openai.ChatCompletionNewParams{
		Model:    parm.ModelName,
		Messages: messages,
		StreamOptions: openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: param.NewOpt(true),
		},
	}
	if parm.Temp != 0 {
		params.Temperature = param.NewOpt(parm.Temp)
	}
	if parm.TopP != 0 {
		params.TopP = param.NewOpt(parm.TopP)
	}
	if parm.MaxToken > 0 {
		params.MaxCompletionTokens = param.NewOpt(parm.MaxToken)
	}
	for _, schema := range req.Tools {
		if schema == nil {
			continue
		}
		params.Tools = append(params.Tools, openai.ChatCompletionFunctionTool(openai.FunctionDefinitionParam{
			Name:        schema.Name,
			Description: openai.String(schema.Description),
			Parameters:  openai.FunctionParameters(schema.Parameters),
		}))
	}
	return params, nil
}

func buildChatCompletionMessages(req *StreamRequest) ([]openai.ChatCompletionMessageParamUnion, error) {
	result := make([]openai.ChatCompletionMessageParamUnion, 0, len(req.Messages)+1)
	if req.SystemPrompt != "" {
		result = append(result, openai.SystemMessage(req.SystemPrompt))
	}
	for _, m := range req.Messages {
		switch m.Role {
		case message.ASSISTANT:
			if len(m.ToolUses) == 0 {
				if m.Content != "" {
					result = append(result, openai.AssistantMessage(m.Content))
				}
				continue
			}
			assistant := openai.ChatCompletionAssistantMessageParam{}
			if m.Content != "" {
				assistant.Content.OfString = param.NewOpt(m.Content)
			}
			for _, toolUse := range m.ToolUses {
				arguments, err := json.Marshal(toolUse.Arguments)
				if err != nil {
					return nil, fmt.Errorf("encode arguments for tool %q: %w", toolUse.ToolName, err)
				}
				assistant.ToolCalls = append(assistant.ToolCalls, openai.ChatCompletionMessageToolCallUnionParam{
					OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
						ID: toolUse.ToolUseID,
						Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
							Name: toolUse.ToolName, Arguments: string(arguments),
						},
					},
				})
			}
			result = append(result, openai.ChatCompletionMessageParamUnion{OfAssistant: &assistant})
		case message.TOOL:
			for _, toolResult := range m.ToolResults {
				result = append(result, openai.ToolMessage(toolResult.Content, toolResult.ToolUseID))
			}
		case message.USER:
			result = append(result, openai.UserMessage(m.Content))
		}
	}
	return result, nil
}
```

调整 `Stream` 对 `buildChatCompletionMessages` 的旧调用，使它暂时改为调用 `buildOpenAIChatCompletionParams`；后续任务会重写完整流编排。

- [ ] **步骤 4：运行请求构建测试并确认通过**

运行：

```bash
go test ./internal/llm -run 'Test(NewOpenAICompatClientRequiresModelName|BuildOpenAIChatCompletionParams)' -count=1
```

预期：PASS。

- [ ] **步骤 5：提交请求构建变更**

```bash
git add internal/llm/openai_compat.go internal/llm/openai_compat_test.go
git commit -m "feat: build complete OpenAI chat requests"
```

## 任务 2：文本、usage 和结束原因状态机

**文件：**

- 创建：`internal/llm/openai_stream_parser.go`
- 修改：`internal/llm/openai_compat_test.go`

- [ ] **步骤 1：编写文本、usage 和结束原因的失败测试**

在测试文件中补充导入 `reflect` 和 `github.com/openai/openai-go/v3`，并加入：

```go
func mustOpenAIChunk(t *testing.T, raw string) openai.ChatCompletionChunk {
	t.Helper()
	var chunk openai.ChatCompletionChunk
	if err := json.Unmarshal([]byte(raw), &chunk); err != nil {
		t.Fatal(err)
	}
	return chunk
}

func TestOpenAIStreamParserTextUsageAndFinishReason(t *testing.T) {
	parser := newOpenAIStreamParser()

	events, err := parser.Consume(mustOpenAIChunk(t, `{
		"choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []StreamEvent{TextStream{Text: "hello"}}) {
		t.Fatalf("unexpected events: %#v", events)
	}

	events, err = parser.Consume(mustOpenAIChunk(t, `{
		"choices":[{"index":0,"delta":{},"finish_reason":"length"}]
	}`))
	if err != nil || len(events) != 0 {
		t.Fatalf("finish chunk: events=%#v err=%v", events, err)
	}

	events, err = parser.Consume(mustOpenAIChunk(t, `{
		"choices":[],
		"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5,
		"prompt_tokens_details":{"cached_tokens":1}}
	}`))
	if err != nil || len(events) != 0 {
		t.Fatalf("usage chunk: events=%#v err=%v", events, err)
	}

	end, err := parser.Finish()
	if err != nil {
		t.Fatal(err)
	}
	want := StreamEnd{
		StopReason: "length",
		Usage: UsageInfo{InputTokens: 3, OutputTokens: 2, TotalTokens: 5, CacheReadTokens: 1},
	}
	if !reflect.DeepEqual(end, want) {
		t.Fatalf("unexpected end: %#v", end)
	}
}

func TestOpenAIStreamParserPassesThroughFinishReasons(t *testing.T) {
	for _, reason := range []string{"stop", "length", "content_filter", "provider_limit"} {
		t.Run(reason, func(t *testing.T) {
			parser := newOpenAIStreamParser()
			_, err := parser.Consume(mustOpenAIChunk(t, `{
				"choices":[{"index":0,"delta":{},"finish_reason":"`+reason+`"}]
			}`))
			if err != nil {
				t.Fatal(err)
			}
			end, err := parser.Finish()
			if err != nil {
				t.Fatal(err)
			}
			if end.StopReason != reason {
				t.Fatalf("got %q, want %q", end.StopReason, reason)
			}
		})
	}
}

func TestOpenAIStreamParserRejectsEOFWithoutFinishReason(t *testing.T) {
	parser := newOpenAIStreamParser()
	_, err := parser.Consume(mustOpenAIChunk(t, `{
		"choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":null}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parser.Finish(); err == nil {
		t.Fatal("expected missing finish reason error")
	}
}
```

- [ ] **步骤 2：运行解析器测试并确认缺少状态机而失败**

运行：

```bash
go test ./internal/llm -run 'TestOpenAIStreamParser(Text|Passes|Rejects)' -count=1
```

预期：FAIL，编译错误包含 `undefined: newOpenAIStreamParser`。

- [ ] **步骤 3：实现文本、usage 和结束原因的最小状态机**

创建 `internal/llm/openai_stream_parser.go`：

```go
package llm

import (
	"fmt"

	"github.com/openai/openai-go/v3"
)

type openAIStreamParser struct {
	stopReason    string
	usage         UsageInfo
	finished      bool
	toolsComplete bool
	toolCalls     map[int64]*openAIToolCallAccum
}

type openAIToolCallAccum struct {
	id            string
	name          string
	arguments     string
	started       bool
	pendingDeltas []string
}

func newOpenAIStreamParser() *openAIStreamParser {
	return &openAIStreamParser{toolCalls: make(map[int64]*openAIToolCallAccum)}
}

func (p *openAIStreamParser) Consume(chunk openai.ChatCompletionChunk) ([]StreamEvent, error) {
	if p.finished {
		return nil, fmt.Errorf("OpenAI stream parser already finished")
	}
	if chunk.JSON.Usage.Valid() {
		p.usage = UsageInfo{
			InputTokens:     chunk.Usage.PromptTokens,
			OutputTokens:    chunk.Usage.CompletionTokens,
			TotalTokens:     chunk.Usage.TotalTokens,
			CacheReadTokens: chunk.Usage.PromptTokensDetails.CachedTokens,
		}
	}
	if len(chunk.Choices) == 0 {
		return nil, nil
	}

	choice := chunk.Choices[0]
	events := make([]StreamEvent, 0, 1)
	if choice.Delta.Content != "" {
		events = append(events, TextStream{Text: choice.Delta.Content})
	}
	if choice.FinishReason != "" {
		p.stopReason = choice.FinishReason
	}
	return events, nil
}

func (p *openAIStreamParser) Finish() (StreamEnd, error) {
	if p.finished {
		return StreamEnd{}, fmt.Errorf("OpenAI stream parser already finished")
	}
	if p.stopReason == "" {
		return StreamEnd{}, fmt.Errorf("OpenAI stream ended without finish reason")
	}
	p.finished = true
	return StreamEnd{StopReason: p.stopReason, Usage: p.usage}, nil
}
```

- [ ] **步骤 4：运行解析器测试并确认通过**

运行：

```bash
go test ./internal/llm -run 'TestOpenAIStreamParser(Text|Passes|Rejects)' -count=1
```

预期：PASS。

- [ ] **步骤 5：提交基础状态机**

```bash
git add internal/llm/openai_stream_parser.go internal/llm/openai_compat_test.go
git commit -m "feat: parse OpenAI text usage and finish reasons"
```

## 任务 3：并行工具调用状态机

**文件：**

- 修改：`internal/llm/openai_stream_parser.go`
- 修改：`internal/llm/openai_compat_test.go`

- [ ] **步骤 1：编写交错工具分片和协议错误的失败测试**

在测试文件中加入：

```go
func TestOpenAIStreamParserAccumulatesInterleavedToolCalls(t *testing.T) {
	parser := newOpenAIStreamParser()
	chunks := []string{
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-a","type":"function","function":{"name":"alpha","arguments":""}}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call-b","type":"function","function":{"name":"beta","arguments":"{\\"b\\":"}}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\\"a\\":1}"}},{"index":1,"function":{"arguments":"2}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	}
	var events []StreamEvent
	for _, raw := range chunks {
		got, err := parser.Consume(mustOpenAIChunk(t, raw))
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, got...)
	}
	want := []StreamEvent{
		ToolCallStart{ToolName: "alpha", ToolID: "call-a"},
		ToolCallStart{ToolName: "beta", ToolID: "call-b"},
		ToolCallStream{ToolID: "call-b", Text: `{"b":`},
		ToolCallStream{ToolID: "call-a", Text: `{"a":1}`},
		ToolCallStream{ToolID: "call-b", Text: `2}`},
		ToolCallComplete{ToolID: "call-a", ToolName: "alpha", Arguments: map[string]any{"a": float64(1)}},
		ToolCallComplete{ToolID: "call-b", ToolName: "beta", Arguments: map[string]any{"b": float64(2)}},
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("unexpected events:\n got: %#v\nwant: %#v", events, want)
	}
	end, err := parser.Finish()
	if err != nil || end.StopReason != "tool_calls" {
		t.Fatalf("unexpected end: %#v, %v", end, err)
	}
}

func TestOpenAIStreamParserTreatsEmptyToolArgumentsAsObject(t *testing.T) {
	parser := newOpenAIStreamParser()
	var events []StreamEvent
	for _, raw := range []string{
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-a","function":{"name":"alpha"}}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	} {
		got, err := parser.Consume(mustOpenAIChunk(t, raw))
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, got...)
	}
	want := ToolCallComplete{ToolID: "call-a", ToolName: "alpha", Arguments: map[string]any{}}
	if len(events) != 2 || !reflect.DeepEqual(events[1], want) {
		t.Fatalf("unexpected events: %#v", events)
	}
}

func TestOpenAIStreamParserBuffersArgumentsUntilToolIdentityIsKnown(t *testing.T) {
	parser := newOpenAIStreamParser()
	chunks := []string{
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\\"value\\":"}}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-a"}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"alpha","arguments":"1}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	}
	var events []StreamEvent
	for _, raw := range chunks {
		got, err := parser.Consume(mustOpenAIChunk(t, raw))
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, got...)
	}
	want := []StreamEvent{
		ToolCallStart{ToolName: "alpha", ToolID: "call-a"},
		ToolCallStream{ToolID: "call-a", Text: `{"value":`},
		ToolCallStream{ToolID: "call-a", Text: `1}`},
		ToolCallComplete{ToolID: "call-a", ToolName: "alpha", Arguments: map[string]any{"value": float64(1)}},
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("unexpected events: %#v", events)
	}
}

func TestOpenAIStreamParserRejectsInvalidToolCalls(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"invalid JSON", `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-a","function":{"name":"alpha","arguments":"{"}}]},"finish_reason":"tool_calls"}]}`},
		{"non-object JSON", `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-a","function":{"name":"alpha","arguments":"[]"}}]},"finish_reason":"tool_calls"}]}`},
		{"missing ID", `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"alpha","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`},
		{"missing name", `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-a","function":{"arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			parser := newOpenAIStreamParser()
			if _, err := parser.Consume(mustOpenAIChunk(t, tc.raw)); err == nil {
				t.Fatal("expected tool-call protocol error")
			}
		})
	}
}
```

- [ ] **步骤 2：运行工具调用测试并确认事件缺失而失败**

运行：

```bash
go test ./internal/llm -run 'TestOpenAIStreamParser(Accumulates|Treats|Buffers|RejectsInvalidTool)' -count=1
```

预期：FAIL；交错工具测试显示缺少 `ToolCallStart`、`ToolCallStream` 和 `ToolCallComplete`，错误用例没有返回错误。

- [ ] **步骤 3：实现工具调用累计、开始事件和完成事件**

在 `internal/llm/openai_stream_parser.go` 中增加 `encoding/json`、`sort` 导入，并在 `Consume` 中处理工具 delta：

```go
for _, delta := range choice.Delta.ToolCalls {
	call := p.toolCalls[delta.Index]
	if call == nil {
		call = &openAIToolCallAccum{}
		p.toolCalls[delta.Index] = call
	}
	if delta.ID != "" {
		call.id = delta.ID
	}
	if delta.Function.Name != "" {
		call.name = delta.Function.Name
	}
	if !call.started && call.id != "" && call.name != "" {
		call.started = true
		events = append(events, ToolCallStart{ToolName: call.name, ToolID: call.id})
		for _, pending := range call.pendingDeltas {
			events = append(events, ToolCallStream{ToolID: call.id, Text: pending})
		}
		call.pendingDeltas = nil
	}
	if delta.Function.Arguments != "" {
		call.arguments += delta.Function.Arguments
		if call.started {
			events = append(events, ToolCallStream{ToolID: call.id, Text: delta.Function.Arguments})
		} else {
			call.pendingDeltas = append(call.pendingDeltas, delta.Function.Arguments)
		}
	}
}
if choice.FinishReason != "" {
	p.stopReason = choice.FinishReason
	if choice.FinishReason == "tool_calls" && !p.toolsComplete {
		completed, err := p.completeToolCalls()
		if err != nil {
			return nil, err
		}
		p.toolsComplete = true
		events = append(events, completed...)
	}
}
```

并增加：

```go
func (p *openAIStreamParser) completeToolCalls() ([]StreamEvent, error) {
	indices := make([]int64, 0, len(p.toolCalls))
	for index := range p.toolCalls {
		indices = append(indices, index)
	}
	sort.Slice(indices, func(i, j int) bool { return indices[i] < indices[j] })

	events := make([]StreamEvent, 0, len(indices))
	for _, index := range indices {
		call := p.toolCalls[index]
		if call.id == "" || call.name == "" {
			return nil, fmt.Errorf("invalid OpenAI tool call at index %d: missing ID or name", index)
		}
		arguments, err := decodeOpenAIToolArguments(call.arguments)
		if err != nil {
			return nil, fmt.Errorf("decode arguments for OpenAI tool %q at index %d: %w", call.name, index, err)
		}
		events = append(events, ToolCallComplete{
			ToolID: call.id, ToolName: call.name, Arguments: arguments,
		})
	}
	return events, nil
}
```

为确保 `[]`、字符串、数字和 `null` 都被拒绝，增加只接受 JSON 对象的解码函数：

```go
func decodeOpenAIToolArguments(raw string) (map[string]any, error) {
	if raw == "" {
		return map[string]any{}, nil
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, err
	}
	arguments, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("must be a JSON object")
	}
	return arguments, nil
}
```

- [ ] **步骤 4：运行工具调用和全部解析器测试**

运行：

```bash
go test ./internal/llm -run 'TestOpenAIStreamParser' -count=1
```

预期：PASS。

- [ ] **步骤 5：提交工具调用状态机**

```bash
git add internal/llm/openai_stream_parser.go internal/llm/openai_compat_test.go
git commit -m "feat: parse parallel OpenAI tool calls"
```

## 任务 4：流生命周期和本地 SSE 集成

**文件：**

- 修改：`internal/llm/openai_compat.go:39-195`
- 修改：`internal/llm/openai_compat_test.go`

- [ ] **步骤 1：编写完整 SSE、异常结束和取消的失败测试**

在测试文件中补充导入 `fmt`、`net/http`、`net/http/httptest`、`strings`、`time`，并加入辅助函数：

```go
func collectOpenAIStream(events <-chan StreamEvent, errs <-chan error) ([]StreamEvent, error) {
	var result []StreamEvent
	for events != nil || errs != nil {
		select {
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			result = append(result, event)
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err != nil {
				return result, err
			}
		}
	}
	return result, nil
}

func newOpenAITestClient(t *testing.T, handler http.HandlerFunc) *OpenAiCompatClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := newOpenAiCompatClient(&ModelParm{
		APIKey: "test-key", BaseURL: server.URL, ModelName: "test-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}
```

加入成功集成测试：

```go
func TestOpenAICompatStreamEndToEnd(t *testing.T) {
	client := newOpenAITestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("unexpected authorization: %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["stream"] != true {
			t.Fatalf("stream not enabled: %#v", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, data := range []string{
			`{"choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	})

	eventCh, errCh := client.Stream(&StreamRequest{Context: context.Background()})
	events, err := collectOpenAIStream(eventCh, errCh)
	if err != nil {
		t.Fatal(err)
	}
	want := []StreamEvent{
		TextStream{Text: "hello"},
		StreamEnd{StopReason: "stop", Usage: UsageInfo{InputTokens: 3, OutputTokens: 1, TotalTokens: 4}},
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("unexpected events: %#v", events)
	}
}
```

加入错误和取消测试：

```go
func TestOpenAICompatStreamRejectsEOFWithoutFinishReason(t *testing.T) {
	client := newOpenAITestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"},\"finish_reason\":null}]}\n\n")
	})
	eventCh, errCh := client.Stream(&StreamRequest{Context: context.Background()})
	_, err := collectOpenAIStream(eventCh, errCh)
	if err == nil || !strings.Contains(err.Error(), "without finish reason") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOpenAICompatStreamReturnsServerError(t *testing.T) {
	client := newOpenAITestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"error\":{\"message\":\"provider failed\"}}\n\n")
	})
	eventCh, errCh := client.Stream(&StreamRequest{Context: context.Background()})
	_, err := collectOpenAIStream(eventCh, errCh)
	if err == nil || !strings.Contains(err.Error(), "provider failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOpenAICompatStreamRejectsNilRequest(t *testing.T) {
	eventCh, errCh := (&OpenAiCompatClient{}).Stream(nil)
	_, err := collectOpenAIStream(eventCh, errCh)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
}

func TestOpenAICompatStreamCancellation(t *testing.T) {
	requestStarted := make(chan struct{})
	client := newOpenAITestClient(t, func(w http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	eventCh, errCh := client.Stream(&StreamRequest{Context: ctx})
	<-requestStarted
	cancel()
	_, err := collectOpenAIStream(eventCh, errCh)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestOpenAICompatStreamIdleTimeout(t *testing.T) {
	oldTimeout := openAIStreamIdleTimeout
	openAIStreamIdleTimeout = 20 * time.Millisecond
	t.Cleanup(func() { openAIStreamIdleTimeout = oldTimeout })

	client := newOpenAITestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	eventCh, errCh := client.Stream(&StreamRequest{Context: context.Background()})
	_, err := collectOpenAIStream(eventCh, errCh)
	if !errors.Is(err, ErrStreamIdleTimeout) {
		t.Fatalf("expected ErrStreamIdleTimeout, got %v", err)
	}
}
```

- [ ] **步骤 2：运行集成测试并确认旧流编排不满足新行为**

运行：

```bash
go test ./internal/llm -run 'TestOpenAICompatStream' -count=1 -timeout=10s
```

预期：FAIL；至少包含 `openAIStreamIdleTimeout` 不可赋值、异常 EOF 没有协议错误，或取消/事件顺序不符合预期。

- [ ] **步骤 3：重写 `Stream` 为可取消的流编排器**

把超时常量改成可由同包测试临时覆盖的变量：

```go
var openAIStreamIdleTimeout = 5 * time.Minute
```

增加内部结果类型和安全计时器函数：

```go
type openAIStreamReadResult struct {
	chunk openai.ChatCompletionChunk
	err   error
	done  bool
}

func resetTimer(timer *time.Timer, duration time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(duration)
}
```

用以下结构替换 `Stream` 的旧实现：

```go
func (c *OpenAiCompatClient) Stream(req *StreamRequest) (<-chan StreamEvent, <-chan error) {
	events := make(chan StreamEvent, 128)
	errs := make(chan error, 1)
	if req == nil || req.Context == nil {
		close(events)
		errs <- fmt.Errorf("%w: request and context are required", ErrInvalidRequest)
		close(errs)
		return events, errs
	}

	go c.runStream(req, events, errs)
	return events, errs
}

func (c *OpenAiCompatClient) runStream(req *StreamRequest, events chan<- StreamEvent, errs chan<- error) {
	defer close(events)
	defer close(errs)

	params, err := buildOpenAIChatCompletionParams(req, c.modelParm)
	if err != nil {
		errs <- err
		return
	}
	ctx, cancel := context.WithCancel(req.Context)
	defer cancel()

	stream := c.client.Chat.Completions.NewStreaming(ctx, params)
	defer stream.Close()
	reads := make(chan openAIStreamReadResult, 1)
	go func() {
		for stream.Next() {
			result := openAIStreamReadResult{chunk: stream.Current()}
			select {
			case reads <- result:
			case <-ctx.Done():
				return
			}
		}
		result := openAIStreamReadResult{err: stream.Err(), done: true}
		select {
		case reads <- result:
		case <-ctx.Done():
		}
	}()

	parser := newOpenAIStreamParser()
	idle := time.NewTimer(openAIStreamIdleTimeout)
	defer idle.Stop()
	for {
		select {
		case <-req.Context.Done():
			cancel()
			errs <- req.Context.Err()
			return
		case <-idle.C:
			cancel()
			errs <- ErrStreamIdleTimeout
			return
		case result := <-reads:
			if result.done {
				if result.err != nil {
					errs <- result.err
					return
				}
				end, err := parser.Finish()
				if err != nil {
					errs <- err
					return
				}
				events <- end
				return
			}
			resetTimer(idle, openAIStreamIdleTimeout)
			parsed, err := parser.Consume(result.chunk)
			if err != nil {
				cancel()
				errs <- err
				return
			}
			for _, event := range parsed {
				select {
				case events <- event:
				case <-req.Context.Done():
					cancel()
					errs <- req.Context.Err()
					return
				}
			}
		}
	}
}
```

实现时将最终 `StreamEnd` 发送也改为监听 `req.Context.Done()` 的 `select`，并用一个 `sendError` 小函数执行非阻塞的单次错误发送，避免调用方停止消费时退出路径阻塞：

```go
func sendOpenAIStreamError(errs chan<- error, err error) {
	select {
	case errs <- err:
	default:
	}
}
```

所有错误出口统一调用 `sendOpenAIStreamError`。

- [ ] **步骤 4：运行集成测试并确认通过**

运行：

```bash
go test ./internal/llm -run 'TestOpenAICompatStream' -count=1 -timeout=10s
```

预期：PASS。

- [ ] **步骤 5：运行竞态检测覆盖流测试**

运行：

```bash
go test -race ./internal/llm -run 'TestOpenAICompatStream' -count=1 -timeout=20s
```

预期：PASS，且无 race 报告。若测试对包级超时变量产生竞态，确保该测试不调用 `t.Parallel()`，并且所有流测试按顺序运行。

- [ ] **步骤 6：提交流生命周期变更**

```bash
git add internal/llm/openai_compat.go internal/llm/openai_compat_test.go
git commit -m "fix: make OpenAI streams cancellation safe"
```

## 任务 5：全量验证和整理

**文件：**

- 修改：`internal/llm/openai_compat.go`
- 修改：`internal/llm/openai_stream_parser.go`
- 修改：`internal/llm/openai_compat_test.go`

- [ ] **步骤 1：格式化所有改动文件**

运行：

```bash
gofmt -w internal/llm/openai_compat.go internal/llm/openai_stream_parser.go internal/llm/openai_compat_test.go
```

预期：命令退出码为 0。

- [ ] **步骤 2：运行协议包测试**

运行：

```bash
go test ./internal/llm -count=1
```

预期：PASS。

- [ ] **步骤 3：运行协议包竞态检测**

运行：

```bash
go test -race ./internal/llm -count=1 -timeout=30s
```

预期：PASS，且无 race 报告。

- [ ] **步骤 4：运行全项目测试**

运行：

```bash
go test ./... -count=1
```

预期：所有包 PASS。

- [ ] **步骤 5：运行静态检查**

运行：

```bash
go vet ./...
```

预期：命令退出码为 0，无诊断输出。

- [ ] **步骤 6：检查差异范围和格式问题**

运行：

```bash
git diff --check
git status --short
git diff --stat HEAD~3
```

预期：`git diff --check` 无输出；状态中不包含与 OpenAI 兼容协议无关的文件。

- [ ] **步骤 7：提交验证阶段的必要整理**

仅当格式化或验证阶段产生代码变更时运行：

```bash
git add internal/llm/openai_compat.go internal/llm/openai_stream_parser.go internal/llm/openai_compat_test.go
git commit -m "test: complete OpenAI compatibility coverage"
```

如果工作区无变更，则跳过该 commit，不创建空提交。
