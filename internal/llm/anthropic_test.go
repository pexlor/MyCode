package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"MyCode/internal/message"
	"MyCode/internal/tool"
)

func TestNewAnthropicClientValidatesConfig(t *testing.T) {
	for _, parm := range []*ModelParm{
		nil,
		{BaseURL: "https://api.anthropic.com", APIKey: "key"},
		{BaseURL: "https://api.anthropic.com", ModelName: "claude-test"},
		{APIKey: "key", ModelName: "claude-test"},
	} {
		if _, err := newAnthropicClient(parm); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("newAnthropicClient(%#v) error = %v, want ErrInvalidConfig", parm, err)
		}
	}
}

func TestBuildAnthropicRequestMapsMessagesAndTools(t *testing.T) {
	req := &StreamRequest{
		Context:      context.Background(),
		SystemPrompt: "be useful",
		Messages: []message.Message{
			{Role: message.USER, Content: "weather?"},
			{Role: message.ASSISTANT, Content: "checking", ToolUses: []message.ToolUseBlock{{
				ToolUseID: "tool-1", ToolName: "weather", Arguments: map[string]any{"city": "Beijing"},
			}}},
			{Role: message.TOOL, ToolResults: []message.ToolResultBlock{{
				ToolUseID: "tool-1", Content: "sunny", IsError: true,
			}}},
		},
		Tools: []*tool.ToolSchema{{
			Name: "weather", Description: "look up weather",
			Parameters: map[string]any{"type": "object"},
		}},
	}

	body, err := buildAnthropicRequest(req, &ModelParm{
		ModelName: "claude-test", MaxToken: 2048, Temp: 0.2, TopP: 0.8, TopK: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["model"] != "claude-test" || got["system"] != "be useful" || got["max_tokens"] != float64(2048) {
		t.Fatalf("unexpected basic fields: %s", raw)
	}
	if got["temperature"] != 0.2 || got["top_p"] != 0.8 || got["top_k"] != float64(20) || got["stream"] != true {
		t.Fatalf("unexpected sampling fields: %s", raw)
	}
	messages := got["messages"].([]any)
	if len(messages) != 3 {
		t.Fatalf("messages length = %d, want 3: %s", len(messages), raw)
	}
	assistantContent := messages[1].(map[string]any)["content"].([]any)
	if assistantContent[0].(map[string]any)["type"] != "text" || assistantContent[1].(map[string]any)["type"] != "tool_use" {
		t.Fatalf("assistant content not mapped: %s", raw)
	}
	toolContent := messages[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	if messages[2].(map[string]any)["role"] != "user" || toolContent["type"] != "tool_result" || toolContent["is_error"] != true {
		t.Fatalf("tool result not mapped: %s", raw)
	}
	tools := got["tools"].([]any)
	if tools[0].(map[string]any)["input_schema"].(map[string]any)["type"] != "object" {
		t.Fatalf("tool schema not mapped: %s", raw)
	}
}

func TestAnthropicClientStreamsTextThinkingToolAndUsage(t *testing.T) {
	var requestPath string
	var apiKey, version string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath = r.URL.Path
		apiKey = r.Header.Get("x-api-key")
		version = r.Header.Get("anthropic-version")
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []struct{ name, data string }{
			{"message_start", `{"type":"message_start","message":{"usage":{"input_tokens":11,"cache_read_input_tokens":3,"cache_creation_input_tokens":2}}}`},
			{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hmm"}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig"}}`},
			{"content_block_stop", `{"type":"content_block_stop","index":0}`},
			{"content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"hello"}}`},
			{"content_block_stop", `{"type":"content_block_stop","index":1}`},
			{"content_block_start", `{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"tool-1","name":"weather","input":{}}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"city\":\"Bei"}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"jing\"}"}}`},
			{"content_block_stop", `{"type":"content_block_stop","index":2}`},
			{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`},
			{"message_stop", `{"type":"message_stop"}`},
		} {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.name, event.data)
		}
	}))
	defer server.Close()

	client, err := newAnthropicClient(&ModelParm{
		BaseURL: server.URL + "/v1", APIKey: "secret", ModelName: "claude-test", MaxToken: 128,
	})
	if err != nil {
		t.Fatal(err)
	}
	events, errs := client.Stream(&StreamRequest{Context: context.Background(), Messages: []message.Message{{Role: message.USER, Content: "hi"}}})
	var got []StreamEvent
	for event := range events {
		got = append(got, event)
	}
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	want := []StreamEvent{
		ThinkingStream{Text: "hmm"},
		ThinkingComplete{Thinking: "hmm", Signature: "sig"},
		TextStream{Text: "hello"},
		ToolCallStart{ToolName: "weather", ToolID: "tool-1"},
		ToolCallStream{ToolID: "tool-1", Text: `{"city":"Bei`},
		ToolCallStream{ToolID: "tool-1", Text: `jing"}`},
		ToolCallComplete{ToolID: "tool-1", ToolName: "weather", Arguments: map[string]any{"city": "Beijing"}},
		StreamEnd{StopReason: "tool_use", Usage: UsageInfo{InputTokens: 11, OutputTokens: 7, TotalTokens: 18, CacheReadTokens: 3, CacheCreationTokens: 2}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %#v\nwant   = %#v", got, want)
	}
	if requestPath != "/v1/messages" || apiKey != "secret" || version != "2023-06-01" {
		t.Fatalf("request metadata = path %q, api key %q, version %q", requestPath, apiKey, version)
	}
}

func TestNewClientSupportsAnthropicProtocol(t *testing.T) {
	client, err := NewClient(&ModelParm{Protocol: "anthropic", BaseURL: "https://api.anthropic.com", APIKey: "key", ModelName: "claude-test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := client.(*AnthropicClient); !ok {
		t.Fatalf("client type = %T, want *AnthropicClient", client)
	}
}
