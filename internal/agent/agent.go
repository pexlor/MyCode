package agent

import (
	contextmanager "MyCode/internal/context"
	"MyCode/internal/llm"
	"MyCode/internal/message"
	"MyCode/internal/tool"
	"context"
	"errors"
	"fmt"
	"strings"
)

const DefaultMaxIterations = 800

type Agent struct {
	ctx            context.Context
	client         llm.LLMClient
	toolManager    *tool.ToolsManager
	contextManager *contextmanager.ContextManager
	sessionID      string
	MaxIterations  int
}

func NewAgent(ctx context.Context, client llm.LLMClient, toolManager *tool.ToolsManager) (*Agent, error) {
	if ctx == nil {
		return nil, errors.New("agent context cannot be nil")
	}
	if client == nil {
		return nil, errors.New("llm client cannot be nil")
	}
	if toolManager == nil {
		toolManager = tool.NewToolsManager()
	}
	return &Agent{
		ctx:           ctx,
		client:        client,
		toolManager:   toolManager,
		MaxIterations: DefaultMaxIterations,
	}, nil
}

func (a *Agent) SetContextManager(manager *contextmanager.ContextManager, sessionID string) {
	// ContextManager 与 SessionID 成对设置：前者决定如何构建视图，后者决定从哪个
	// transcript 和摘要检查点恢复上下文。
	a.contextManager = manager
	a.sessionID = sessionID
}

// SetThinkingEnabled updates the mode used by subsequent model requests.
func (a *Agent) SetThinkingEnabled(enabled bool) error {
	controller, ok := a.client.(llm.ThinkingModeController)
	if !ok {
		return errors.New("the configured model protocol does not support toggling thinking mode")
	}
	controller.SetThinkingEnabled(enabled)
	return nil
}

func (a *Agent) ThinkingEnabled() (bool, error) {
	controller, ok := a.client.(llm.ThinkingModeController)
	if !ok {
		return false, errors.New("the configured model protocol does not support toggling thinking mode")
	}
	return controller.ThinkingEnabled(), nil
}

// Run executes the agent loop and emits events for upper-layer UI.
func (a *Agent) Run(mm *message.MessageManager) <-chan AgentEvent {
	return a.RunContext(agentContext(a), mm)
}

// RunContext executes one agent turn with a caller-controlled context. This
// lets an interactive UI interrupt an in-flight model request or tool call.
func (a *Agent) RunContext(ctx context.Context, mm *message.MessageManager) <-chan AgentEvent {
	agentEventCh := make(chan AgentEvent, 32)
	if ctx == nil {
		ctx = agentContext(a)
	}

	go func() {
		defer close(agentEventCh)
		var totalUsage llm.UsageInfo

		if err := a.validate(mm); err != nil {
			sendAgentEvent(ctx, agentEventCh, ErrorEvent{Err: err})
			return
		}

		for iteration := 0; iteration < a.MaxIterations; iteration++ {
			toolSchemas := a.toolManager.BuildAllSchemas()
			systemPrompt := mm.SystemPrompt
			history := mm.History
			if a.contextManager != nil {
				// 每次模型请求（包括同一 Turn 中的工具循环）都重新构建 ContextView。
				// Build 内部通过同步游标避免重复写 transcript，并通过摘要检查点避免重复压缩。
				view, err := a.contextManager.Build(ctx, contextmanager.BuildInput{
					SessionID: a.sessionID, SystemPrompt: mm.SystemPrompt,
					CurrentRequest: latestUserRequest(mm.History), History: mm.History,
					AvailableTools: toolSchemas,
				})
				if err != nil {
					sendAgentEvent(ctx, agentEventCh, ErrorEvent{Err: err})
					return
				}
				// 从这里开始，LLM 只接触经过预算治理的视图，不再直接接触完整 History。
				systemPrompt = view.SystemPrompt
				history = view.Messages
				toolSchemas = view.Tools
			}
			sendAgentEvent(ctx, agentEventCh, ThinkingStartEvent{})
			events, errs := a.client.Stream(&llm.StreamRequest{
				Context:      ctx,
				SystemPrompt: systemPrompt,
				Messages:     history,
				Tools:        toolSchemas,
			})

			assistantText, toolCalls, stopReason, usage, err := a.handleStream(ctx, events, errs, agentEventCh)
			if err != nil {
				sendAgentEvent(ctx, agentEventCh, ErrorEvent{Err: err})
				return
			}

			if len(toolCalls) == 0 {
				if assistantText != "" {
					mm.History = append(mm.History, message.Message{
						Role:    message.ASSISTANT,
						Content: assistantText,
					})
				}
				if a.contextManager != nil {
					if err := a.contextManager.SyncHistory(ctx, a.sessionID, mm.History); err != nil {
						sendAgentEvent(ctx, agentEventCh, ErrorEvent{Err: err})
						return
					}
				}
				sendAgentEvent(ctx, agentEventCh, DoneEvent{StopReason: stopReason, Usage: addUsage(totalUsage, usage)})
				return
			}
			totalUsage = addUsage(totalUsage, usage)

			mm.History = append(mm.History, message.Message{
				Role:     message.ASSISTANT,
				Content:  assistantText,
				ToolUses: toToolUseBlocks(toolCalls),
			})

			toolResults := a.executeTools(ctx, toolCalls, agentEventCh)
			mm.AddToolResult(toolResults)
		}

		sendAgentEvent(ctx, agentEventCh, ErrorEvent{Err: fmt.Errorf("agent loop exceeded max iterations %d", a.MaxIterations)})
	}()

	return agentEventCh
}

func latestUserRequest(history []message.Message) string {
	// 工具循环会在用户消息后追加多条 assistant/tool 消息，因此必须逆序寻找最近用户请求。
	for index := len(history) - 1; index >= 0; index-- {
		if history[index].Role == message.USER {
			return history[index].Content
		}
	}
	return ""
}

func (a *Agent) run(mm *message.MessageManager) <-chan AgentEvent {
	return a.Run(mm)
}

func (a *Agent) validate(mm *message.MessageManager) error {
	if a == nil {
		return errors.New("agent cannot be nil")
	}
	if a.ctx == nil {
		return errors.New("agent context cannot be nil")
	}
	if a.client == nil {
		return errors.New("llm client cannot be nil")
	}
	if a.toolManager == nil {
		return errors.New("tool manager cannot be nil")
	}
	if mm == nil {
		return errors.New("message manager cannot be nil")
	}
	if a.MaxIterations <= 0 {
		return errors.New("max iterations must be greater than zero")
	}
	return nil
}

func (a *Agent) handleStream(ctx context.Context, events <-chan llm.StreamEvent, errs <-chan error, out chan<- AgentEvent) (string, []llm.ToolCallComplete, string, llm.UsageInfo, error) {
	var assistantText strings.Builder
	var toolCalls []llm.ToolCallComplete
	stopReason := ""
	var usage llm.UsageInfo

	for events != nil || errs != nil {
		select {
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			switch ev := event.(type) {
			case llm.TextStream:
				assistantText.WriteString(ev.Text)
				sendAgentEvent(ctx, out, TextEvent{Text: ev.Text})
			case llm.ThinkingStream:
				sendAgentEvent(ctx, out, ThinkingEvent{Text: ev.Text})
			case llm.ToolCallStart:
				sendAgentEvent(ctx, out, ToolCallStartEvent{ToolUseID: ev.ToolID, ToolName: ev.ToolName})
			case llm.ToolCallStream:
				sendAgentEvent(ctx, out, ToolCallDeltaEvent{ToolUseID: ev.ToolID, Text: ev.Text})
			case llm.ToolCallComplete:
				toolCalls = append(toolCalls, ev)
				sendAgentEvent(ctx, out, ToolCallCompleteEvent{ToolUseID: ev.ToolID, ToolName: ev.ToolName, Arguments: ev.Arguments})
			case llm.StreamEnd:
				stopReason = ev.StopReason
				usage = ev.Usage
			}
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err != nil {
				return assistantText.String(), toolCalls, stopReason, usage, err
			}
		case <-ctx.Done():
			return assistantText.String(), toolCalls, stopReason, usage, ctx.Err()
		}
	}

	return assistantText.String(), toolCalls, stopReason, usage, nil
}

func addUsage(u, other llm.UsageInfo) llm.UsageInfo {
	return llm.UsageInfo{
		InputTokens:         u.InputTokens + other.InputTokens,
		OutputTokens:        u.OutputTokens + other.OutputTokens,
		TotalTokens:         u.TotalTokens + other.TotalTokens,
		CacheReadTokens:     u.CacheReadTokens + other.CacheReadTokens,
		CacheCreationTokens: u.CacheCreationTokens + other.CacheCreationTokens,
	}
}

func (a *Agent) executeTool(ctx context.Context, call llm.ToolCallComplete) tool.ToolResult {
	if call.ToolID == "" || call.ToolName == "" {
		return tool.ToolResult{Output: "tool call is missing id or name", IsError: true}
	}

	return a.toolManager.Execute(ctx, call.ToolName, call.Arguments)
}

type completedToolCall struct {
	index  int
	call   llm.ToolCallComplete
	result tool.ToolResult
}

// executeTools starts every tool call in an iteration concurrently. Results are
// returned in call order because tool-result protocols associate the response
// sequence with the corresponding assistant tool-use sequence.
func (a *Agent) executeTools(ctx context.Context, calls []llm.ToolCallComplete, events chan<- AgentEvent) []message.ToolResultBlock {
	completed := make(chan completedToolCall, len(calls))
	for index, call := range calls {
		sendAgentEvent(ctx, events, ToolExecutionStartEvent{
			ToolUseID: call.ToolID,
			ToolName:  call.ToolName,
		})
		go func(index int, call llm.ToolCallComplete) {
			completed <- completedToolCall{index: index, call: call, result: a.executeTool(ctx, call)}
		}(index, call)
	}

	results := make([]message.ToolResultBlock, len(calls))
	for range calls {
		var outcome completedToolCall
		select {
		case outcome = <-completed:
		case <-ctx.Done():
			return results
		}
		results[outcome.index] = message.ToolResultBlock{
			ToolUseID: outcome.call.ToolID,
			Content:   outcome.result.Output,
			IsError:   outcome.result.IsError,
		}
		sendAgentEvent(ctx, events, ToolResultEvent{
			ToolUseID: outcome.call.ToolID,
			ToolName:  outcome.call.ToolName,
			Content:   outcome.result.Output,
			IsError:   outcome.result.IsError,
		})
	}
	return results
}

func toToolUseBlocks(toolCalls []llm.ToolCallComplete) []message.ToolUseBlock {
	toolUses := make([]message.ToolUseBlock, 0, len(toolCalls))
	for _, call := range toolCalls {
		toolUses = append(toolUses, message.ToolUseBlock{
			ToolUseID: call.ToolID,
			ToolName:  call.ToolName,
			Arguments: call.Arguments,
		})
	}
	return toolUses
}

func sendAgentEvent(ctx context.Context, ch chan<- AgentEvent, event AgentEvent) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return false
	}
	select {
	case ch <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

func agentContext(a *Agent) context.Context {
	if a == nil || a.ctx == nil {
		return context.Background()
	}
	return a.ctx
}
