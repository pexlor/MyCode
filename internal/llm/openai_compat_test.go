package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAICompatPassesEnableThinkingAndReadsReasoningContent(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/chat/completions" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if err := json.NewDecoder(request.Body).Decode(&requestBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(writer, "data: {\"id\":\"test\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"qwen-plus\",\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"considering\",\"content\":\"answer\"},\"finish_reason\":null}]}\n\n")
		fmt.Fprint(writer, "data: {\"id\":\"test\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"qwen-plus\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
	}))
	defer server.Close()

	client, err := newOpenAiCompatClient(&ModelParm{BaseURL: server.URL, APIKey: "test", ModelName: "qwen-plus", EnableThinking: true})
	if err != nil {
		t.Fatal(err)
	}
	events, errors := client.Stream(&StreamRequest{Context: context.Background()})
	var thinking, text string
	for event := range events {
		switch event := event.(type) {
		case ThinkingStream:
			thinking += event.Text
		case TextStream:
			text += event.Text
		}
	}
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if enabled, ok := requestBody["enable_thinking"].(bool); !ok || !enabled {
		t.Fatalf("enable_thinking = %#v, want true", requestBody["enable_thinking"])
	}
	if thinking != "considering" || text != "answer" {
		t.Fatalf("thinking = %q, text = %q", thinking, text)
	}
}

func TestOpenAICompatThinkingDeltaIgnoresMalformedData(t *testing.T) {
	if got := openAICompatThinkingDelta("not json"); got != "" {
		t.Fatalf("thinking delta = %q, want empty", got)
	}
}

func TestOpenAICompatThinkingModeCanChangeBetweenRequests(t *testing.T) {
	client, err := newOpenAiCompatClient(&ModelParm{BaseURL: "https://example.com", APIKey: "test", ModelName: "qwen-plus"})
	if err != nil {
		t.Fatal(err)
	}
	if client.ThinkingEnabled() {
		t.Fatal("thinking starts enabled")
	}
	client.SetThinkingEnabled(true)
	if !client.ThinkingEnabled() {
		t.Fatal("thinking was not enabled")
	}
}
