package repl

import (
	"MyCode/internal/agent"
	"MyCode/internal/llm"
	"MyCode/internal/message"
	"MyCode/internal/prompt"
	"MyCode/internal/tool"
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
)

const (
	defaultBaseURL   = "https://llm-lgsv9uhdbprfr0vv.cn-beijing.maas.aliyuncs.com/compatible-mode/v1"
	defaultModelName = "qwen-plus"
)

func REPL() {
	// 交互模式
	runInteractive()
}

func runInteractive() {
	// ctx := context.Background()
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("mycode started. Type /exit to quit.")
	runner, _ := initAgent()
	messageManager, _ := initMessageManager()
	for {
		fmt.Println("input: ")
		userInput, err := reader.ReadString('\n')
		if err != nil {
			fmt.Println("read input error:", err)
			return
		}
		messageManager.AddText(userInput)
		for event := range runner.Run(messageManager) {
			if err := handleAgentEvent(event); err != nil {
				fmt.Println(err)
				return
			}
		}
	}
}

func initAgent() (*agent.Agent, error) {
	// 后续支持可修改
	apiKey := "sk-72683ab6f2174c81bc7d05d13b4c7296"
	protocol := "openai-compat"
	baseURL := defaultBaseURL
	modelName := defaultModelName

	// 构造llm客户端
	client, err := llm.NewClient(&llm.ModelParm{
		Protocol:  protocol,
		BaseURL:   baseURL,
		APIKey:    apiKey,
		ModelName: modelName,
	})

	if err != nil {
		fmt.Println("创建客户端失败: ", err)
		return nil, err
	}

	ctx := context.Background()

	tools := tool.CreateDefaultTools()
	// todo : 后续支持工具筛选

	runner, err := agent.NewAgent(ctx, client, tools)
	if err != nil {
		fmt.Println("创建 agent 失败", err)
		return nil, err
	}
	return runner, nil
}

func initMessageManager() (*message.MessageManager, error) {
	SystemPrompt, err := prompt.BuildSystemPrompt()
	if err != nil {
		fmt.Println(err)
		return nil, err
	}
	return &message.MessageManager{
		SystemPrompt: SystemPrompt,
	}, nil
}

func handleAgentEvent(event agent.AgentEvent) error {
	switch ev := event.(type) {
	case agent.TextEvent:
		fmt.Print(ev.Text)
	case agent.ThinkingEvent:
		fmt.Fprint(os.Stderr, ev.Text)
	case agent.ToolCallStartEvent:
		fmt.Fprintf(os.Stderr, "\n[tool] start %s (%s)\n", ev.ToolName, ev.ToolUseID)
	case agent.ToolCallCompleteEvent:
		fmt.Fprintf(os.Stderr, "[tool] args %s: %v\n", ev.ToolName, ev.Arguments)
	case agent.ToolResultEvent:
		status := "ok"
		if ev.IsError {
			status = "error"
		}
		fmt.Fprintf(os.Stderr, "[tool] result %s %s (%d chars)\n", ev.ToolName, status, len(ev.Content))
	case agent.DoneEvent:
		fmt.Fprintf(os.Stderr, "\n[agent] done: %s\n", ev.StopReason)
	case agent.ErrorEvent:
		return ev.Err
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
