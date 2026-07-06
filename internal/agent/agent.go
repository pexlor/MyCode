package agent

import (
	"MyCode/internal/llm"
	"MyCode/internal/message"
	"MyCode/internal/tool"
	"context"
	"fmt"
)

type Agent struct {
	ctx           context.Context
	client        llm.LLMClient
	toolManager   *tool.ToolsManager
	MaxIterations int
}

func NewAgent() {

}

// agent loop
// 输出event用于上层ui
func (a *Agent) run(mm *message.MessageManager) <-chan AgentEvent {
	agentEventCh := make(chan AgentEvent, 32)

	go func() {
		defer close(agentEventCh)
		iteration := 1
		for iteration < a.MaxIterations {
			// loop step one: 对话
			// loop step two: 工具调用
			// loop step three: 写入工具调用结果
			toolSchemas := a.toolManager.BuildAllSchemas()
			events, errs := a.client.Stream(&llm.StreamRequest{
				Context:      a.ctx,
				SystemPrompt: mm.SystemPrompt,
				Messages:     mm.History,
				Tools:        toolSchemas,
			})
			var toolCalls []llm.ToolCallComplete
			a.eventsHandle(events, errs)

			if len(toolCalls) == 0 {
				// 没有工具调用说明agentloop结束
				return
			}

			iteration++
		}
		// 超过最大迭代次数
	}()
	return agentEventCh
}

func (a *Agent) eventsHandle(events <-chan llm.StreamEvent, errs <-chan error) {
	text := ""
	select {
	case event := <-events:
		switch ev := event.(type) {
		case llm.TextStream:
			text += ev.Text
		case llm.StreamEnd:
			return
		}
	case err := <-errs:
		fmt.Println(err)
	}
}
