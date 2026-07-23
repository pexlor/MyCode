package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"MyCode/internal/message"
)

const (
	anthropicAPIVersion       = "2023-06-01"
	defaultAnthropicMaxTokens = int64(4096)
)

type AnthropicClient struct {
	httpClient *http.Client
	modelParm  *ModelParm
	endpoint   string
}

type anthropicRequest struct {
	Model       string             `json:"model"`
	Messages    []anthropicMessage `json:"messages"`
	System      string             `json:"system,omitempty"`
	MaxTokens   int64              `json:"max_tokens"`
	Temperature *float64           `json:"temperature,omitempty"`
	TopP        *float64           `json:"top_p,omitempty"`
	TopK        *float64           `json:"top_k,omitempty"`
	Tools       []anthropicTool    `json:"tools,omitempty"`
	Stream      bool               `json:"stream"`
}

type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

type anthropicContentBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	Thinking  string         `json:"thinking,omitempty"`
	Signature string         `json:"signature,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   string         `json:"content,omitempty"`
	IsError   bool           `json:"is_error,omitempty"`
}

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

func newAnthropicClient(parm *ModelParm) (*AnthropicClient, error) {
	if parm == nil {
		return nil, fmt.Errorf("%w: model parameters cannot be nil", ErrInvalidConfig)
	}
	if parm.APIKey == "" || parm.BaseURL == "" || parm.ModelName == "" {
		return nil, fmt.Errorf("%w: APIKey, BaseURL and ModelName are required", ErrInvalidConfig)
	}
	endpoint, err := anthropicMessagesEndpoint(parm.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid BaseURL: %v", ErrInvalidConfig, err)
	}
	return &AnthropicClient{httpClient: http.DefaultClient, modelParm: parm, endpoint: endpoint}, nil
}

func anthropicMessagesEndpoint(baseURL string) (string, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("must be an absolute URL")
	}
	switch {
	case strings.HasSuffix(u.Path, "/v1/messages"):
	case strings.HasSuffix(u.Path, "/v1"):
		u.Path += "/messages"
	default:
		u.Path += "/v1/messages"
	}
	return u.String(), nil
}

func buildAnthropicRequest(req *StreamRequest, parm *ModelParm) (anthropicRequest, error) {
	if req == nil || parm == nil {
		return anthropicRequest{}, fmt.Errorf("%w: request and model parameters are required", ErrInvalidRequest)
	}
	maxTokens := parm.MaxToken
	if maxTokens <= 0 {
		maxTokens = defaultAnthropicMaxTokens
	}
	body := anthropicRequest{
		Model: parm.ModelName, System: req.SystemPrompt, MaxTokens: maxTokens, Stream: true,
	}
	if parm.Temp != 0 {
		body.Temperature = &parm.Temp
	}
	if parm.TopP != 0 {
		body.TopP = &parm.TopP
	}
	if parm.TopK != 0 {
		body.TopK = &parm.TopK
	}
	for _, schema := range req.Tools {
		if schema == nil {
			continue
		}
		body.Tools = append(body.Tools, anthropicTool{Name: schema.Name, Description: schema.Description, InputSchema: schema.Parameters})
	}
	for _, msg := range req.Messages {
		converted := anthropicMessage{}
		switch msg.Role {
		case message.USER:
			converted.Role = "user"
			if msg.Content != "" {
				converted.Content = append(converted.Content, anthropicContentBlock{Type: "text", Text: msg.Content})
			}
		case message.ASSISTANT:
			converted.Role = "assistant"
			for _, thinking := range msg.ThinkingBlocks {
				converted.Content = append(converted.Content, anthropicContentBlock{Type: "thinking", Thinking: thinking.Thinking})
			}
			if msg.Content != "" {
				converted.Content = append(converted.Content, anthropicContentBlock{Type: "text", Text: msg.Content})
			}
			for _, use := range msg.ToolUses {
				converted.Content = append(converted.Content, anthropicContentBlock{Type: "tool_use", ID: use.ToolUseID, Name: use.ToolName, Input: use.Arguments})
			}
		case message.TOOL:
			converted.Role = "user"
			for _, result := range msg.ToolResults {
				converted.Content = append(converted.Content, anthropicContentBlock{Type: "tool_result", ToolUseID: result.ToolUseID, Content: result.Content, IsError: result.IsError})
			}
		default:
			continue
		}
		if len(converted.Content) > 0 {
			body.Messages = append(body.Messages, converted)
		}
	}
	if _, err := json.Marshal(body); err != nil {
		return anthropicRequest{}, fmt.Errorf("encode Anthropic request: %w", err)
	}
	return body, nil
}

func (c *AnthropicClient) Stream(req *StreamRequest) (<-chan StreamEvent, <-chan error) {
	events := make(chan StreamEvent, 128)
	errs := make(chan error, 1)
	if req == nil || req.Context == nil {
		close(events)
		errs <- fmt.Errorf("%w: request and context are required", ErrInvalidRequest)
		close(errs)
		return events, errs
	}
	go func() {
		defer close(events)
		defer close(errs)
		body, err := buildAnthropicRequest(req, c.modelParm)
		if err != nil {
			errs <- err
			return
		}
		payload, err := json.Marshal(body)
		if err != nil {
			errs <- fmt.Errorf("encode Anthropic request: %w", err)
			return
		}
		httpReq, err := http.NewRequestWithContext(req.Context, http.MethodPost, c.endpoint, bytes.NewReader(payload))
		if err != nil {
			errs <- err
			return
		}
		httpReq.Header.Set("content-type", "application/json")
		httpReq.Header.Set("accept", "text/event-stream")
		httpReq.Header.Set("x-api-key", c.modelParm.APIKey)
		httpReq.Header.Set("anthropic-version", anthropicAPIVersion)
		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			errs <- err
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			errs <- fmt.Errorf("Anthropic API returned %s: %s", resp.Status, strings.TrimSpace(string(data)))
			return
		}
		if err := consumeAnthropicStream(req.Context, resp.Body, events); err != nil {
			errs <- err
		}
	}()
	return events, errs
}

type anthropicStreamState struct {
	blocks     map[int]*anthropicBlockState
	usage      UsageInfo
	stopReason string
}

type anthropicBlockState struct {
	typ, id, name         string
	text, signature, json string
}

type anthropicSSEEvent struct {
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentBlock struct {
		Type     string `json:"type"`
		ID       string `json:"id"`
		Name     string `json:"name"`
		Text     string `json:"text"`
		Thinking string `json:"thinking"`
	} `json:"content_block"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		Signature   string `json:"signature"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Message struct {
		Usage anthropicUsage `json:"usage"`
	} `json:"message"`
	Usage anthropicUsage `json:"usage"`
	Error struct {
		Type, Message string
	} `json:"error"`
}

type anthropicUsage struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheReadTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
}

func consumeAnthropicStream(ctx context.Context, reader io.Reader, output chan<- StreamEvent) error {
	state := anthropicStreamState{blocks: make(map[int]*anthropicBlockState)}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var event anthropicSSEEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return fmt.Errorf("decode Anthropic stream event: %w", err)
		}
		events, done, err := state.consume(event)
		if err != nil {
			return err
		}
		for _, streamEvent := range events {
			select {
			case output <- streamEvent:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if done {
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return fmt.Errorf("Anthropic stream ended before message_stop")
}

func (s *anthropicStreamState) consume(event anthropicSSEEvent) ([]StreamEvent, bool, error) {
	switch event.Type {
	case "message_start":
		s.updateUsage(event.Message.Usage)
	case "content_block_start":
		block := &anthropicBlockState{typ: event.ContentBlock.Type, id: event.ContentBlock.ID, name: event.ContentBlock.Name, text: event.ContentBlock.Thinking}
		s.blocks[event.Index] = block
		if block.typ == "tool_use" {
			return []StreamEvent{ToolCallStart{ToolName: block.name, ToolID: block.id}}, false, nil
		}
	case "content_block_delta":
		block := s.blocks[event.Index]
		if block == nil {
			return nil, false, fmt.Errorf("Anthropic delta references unknown content block %d", event.Index)
		}
		switch event.Delta.Type {
		case "text_delta":
			return []StreamEvent{TextStream{Text: event.Delta.Text}}, false, nil
		case "thinking_delta":
			block.text += event.Delta.Thinking
			return []StreamEvent{ThinkingStream{Text: event.Delta.Thinking}}, false, nil
		case "signature_delta":
			block.signature += event.Delta.Signature
		case "input_json_delta":
			block.json += event.Delta.PartialJSON
			return []StreamEvent{ToolCallStream{ToolID: block.id, Text: event.Delta.PartialJSON}}, false, nil
		}
	case "content_block_stop":
		block := s.blocks[event.Index]
		if block == nil {
			return nil, false, fmt.Errorf("Anthropic stop references unknown content block %d", event.Index)
		}
		delete(s.blocks, event.Index)
		switch block.typ {
		case "thinking":
			return []StreamEvent{ThinkingComplete{Thinking: block.text, Signature: block.signature}}, false, nil
		case "tool_use":
			input := make(map[string]any)
			if block.json != "" {
				if err := json.Unmarshal([]byte(block.json), &input); err != nil {
					return nil, false, fmt.Errorf("decode Anthropic tool %q input: %w", block.name, err)
				}
			}
			return []StreamEvent{ToolCallComplete{ToolID: block.id, ToolName: block.name, Arguments: input}}, false, nil
		}
	case "message_delta":
		if event.Delta.StopReason != "" {
			s.stopReason = event.Delta.StopReason
		}
		s.updateUsage(event.Usage)
	case "message_stop":
		if s.stopReason == "" {
			return nil, false, fmt.Errorf("Anthropic message_stop missing stop_reason")
		}
		return []StreamEvent{StreamEnd{StopReason: s.stopReason, Usage: s.usage}}, true, nil
	case "error":
		return nil, false, fmt.Errorf("Anthropic stream error %s: %s", event.Error.Type, event.Error.Message)
	case "ping":
	default:
		return nil, false, fmt.Errorf("unknown Anthropic stream event type %q", event.Type)
	}
	return nil, false, nil
}

func (s *anthropicStreamState) updateUsage(usage anthropicUsage) {
	if usage.InputTokens != 0 {
		s.usage.InputTokens = usage.InputTokens
	}
	if usage.OutputTokens != 0 {
		s.usage.OutputTokens = usage.OutputTokens
	}
	if usage.CacheReadTokens != 0 {
		s.usage.CacheReadTokens = usage.CacheReadTokens
	}
	if usage.CacheCreationTokens != 0 {
		s.usage.CacheCreationTokens = usage.CacheCreationTokens
	}
	s.usage.TotalTokens = s.usage.InputTokens + s.usage.OutputTokens
}
