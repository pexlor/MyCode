package agent

import (
	"MyCode/internal/llm"
	"MyCode/internal/message"
	"MyCode/internal/tool"
	"context"
	"errors"
	"fmt"
	"strings"
)

const DefaultMaxIterations = 8

type Agent struct {
	ctx           context.Context
	client        llm.LLMClient
	toolManager   *tool.ToolsManager
	MaxIterations int
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

// Run executes the agent loop and emits events for upper-layer UI.
func (a *Agent) Run(mm *message.MessageManager) <-chan AgentEvent {
	agentEventCh := make(chan AgentEvent, 32)
	ctx := agentContext(a)

	go func() {
		defer close(agentEventCh)

		if err := a.validate(mm); err != nil {
			sendAgentEvent(ctx, agentEventCh, ErrorEvent{Err: err})
			return
		}

		for iteration := 0; iteration < a.MaxIterations; iteration++ {
			toolSchemas := a.toolManager.BuildAllSchemas()
			events, errs := a.client.Stream(&llm.StreamRequest{
				Context:      a.ctx,
				SystemPrompt: mm.SystemPrompt,
				Messages:     mm.History,
				Tools:        toolSchemas,
			})

			assistantText, toolCalls, stopReason, err := a.handleStream(events, errs, agentEventCh)
			if err != nil {
				sendAgentEvent(a.ctx, agentEventCh, ErrorEvent{Err: err})
				return
			}

			if len(toolCalls) == 0 {
				if assistantText != "" {
					mm.History = append(mm.History, message.Message{
						Role:    message.ASSISTANT,
						Content: assistantText,
					})
				}
				sendAgentEvent(a.ctx, agentEventCh, DoneEvent{StopReason: stopReason})
				return
			}

			mm.History = append(mm.History, message.Message{
				Role:     message.ASSISTANT,
				Content:  assistantText,
				ToolUses: toToolUseBlocks(toolCalls),
			})

			toolResults := make([]message.ToolResultBlock, 0, len(toolCalls))
			for _, call := range toolCalls {
				result := a.executeTool(call)
				toolResults = append(toolResults, message.ToolResultBlock{
					ToolUseID: call.ToolID,
					Content:   result.Output,
					IsError:   result.IsError,
				})
				sendAgentEvent(a.ctx, agentEventCh, ToolResultEvent{
					ToolUseID: call.ToolID,
					ToolName:  call.ToolName,
					Content:   result.Output,
					IsError:   result.IsError,
				})
			}
			mm.AddToolResult(toolResults)
		}

		sendAgentEvent(a.ctx, agentEventCh, ErrorEvent{Err: fmt.Errorf("agent loop exceeded max iterations %d", a.MaxIterations)})
	}()

	return agentEventCh
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

func (a *Agent) handleStream(events <-chan llm.StreamEvent, errs <-chan error, out chan<- AgentEvent) (string, []llm.ToolCallComplete, string, error) {
	var assistantText strings.Builder
	var toolCalls []llm.ToolCallComplete
	stopReason := ""

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
				sendAgentEvent(a.ctx, out, TextEvent{Text: ev.Text})
			case llm.ThinkingStream:
				sendAgentEvent(a.ctx, out, ThinkingEvent{Text: ev.Text})
			case llm.ToolCallStart:
				sendAgentEvent(a.ctx, out, ToolCallStartEvent{ToolUseID: ev.ToolID, ToolName: ev.ToolName})
			case llm.ToolCallStream:
				sendAgentEvent(a.ctx, out, ToolCallDeltaEvent{ToolUseID: ev.ToolID, Text: ev.Text})
			case llm.ToolCallComplete:
				toolCalls = append(toolCalls, ev)
				sendAgentEvent(a.ctx, out, ToolCallCompleteEvent{ToolUseID: ev.ToolID, ToolName: ev.ToolName, Arguments: ev.Arguments})
			case llm.StreamEnd:
				stopReason = ev.StopReason
			}
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err != nil {
				return assistantText.String(), toolCalls, stopReason, err
			}
		case <-a.ctx.Done():
			return assistantText.String(), toolCalls, stopReason, a.ctx.Err()
		}
	}

	return assistantText.String(), toolCalls, stopReason, nil
}

func (a *Agent) executeTool(call llm.ToolCallComplete) tool.ToolResult {
	if call.ToolID == "" || call.ToolName == "" {
		return tool.ToolResult{Output: "tool call is missing id or name", IsError: true}
	}

	registeredTool := a.toolManager.GetTool(call.ToolName)
	if registeredTool == nil {
		return tool.ToolResult{Output: fmt.Sprintf("tool %q is not registered", call.ToolName), IsError: true}
	}

	return registeredTool.Execute(a.ctx, call.Arguments)
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
