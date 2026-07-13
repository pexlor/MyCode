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

const (
	colorReset = "\033[0m"
	colorDim   = "\033[2m"
	colorCyan  = "\033[36m"
	colorGreen = "\033[32m"
	colorRed   = "\033[31m"
	colorGray  = "\033[90m"
)

func REPL() {
	// 交互模式
	runInteractive()
}

func runInteractive() {
	reader := bufio.NewReader(os.Stdin)

	printWelcome()

	runner, err := initAgent()
	if err != nil {
		printError("agent 初始化失败", err)
		return
	}

	messageManager, err := initMessageManager()
	if err != nil {
		printError("消息初始化失败", err)
		return
	}

	for {
		fmt.Print(promptLabel())
		userInput, err := reader.ReadString('\n')
		if err != nil {
			printError("读取输入失败", err)
			return
		}

		userInput = strings.TrimSpace(userInput)
		if userInput == "" {
			continue
		}

		if handled, quit := handleCommand(userInput); handled {
			if quit {
				fmt.Println(dim("bye."))
				return
			}
			continue
		}

		messageManager.AddText(userInput)
		fmt.Print(assistantLabel())
		for event := range runner.Run(messageManager) {
			if err := handleAgentEvent(event); err != nil {
				printError("执行失败", err)
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
		return nil, err
	}

	ctx := context.Background()

	tools := tool.CreateDefaultTools()
	// todo : 后续支持工具筛选

	runner, err := agent.NewAgent(ctx, client, tools)
	if err != nil {
		return nil, err
	}
	return runner, nil
}

func initMessageManager() (*message.MessageManager, error) {
	SystemPrompt, err := prompt.BuildSystemPrompt()
	if err != nil {
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
		if strings.TrimSpace(ev.Text) != "" {
			fmt.Fprintf(os.Stderr, "%s%s%s", colorGray, ev.Text, colorReset)
		}
	case agent.ToolCallStartEvent:
		fmt.Fprintf(os.Stderr, "\n%s%s%s\n", colorDim, toolLine("running", ev.ToolName), colorReset)
	case agent.ToolCallCompleteEvent:
		if len(ev.Arguments) > 0 {
			fmt.Fprintf(os.Stderr, "%s%s%s\n", colorDim, toolLine("args", ev.ToolName), colorReset)
		}
	case agent.ToolResultEvent:
		status := "ok"
		color := colorGreen
		if ev.IsError {
			status = "error"
			color = colorRed
		}
		fmt.Fprintf(os.Stderr, "%s%s%s %s%s%s\n", colorDim, toolLine("result", ev.ToolName), colorReset, color, status, colorReset)
	case agent.DoneEvent:
		if ev.StopReason != "" {
			fmt.Fprintf(os.Stderr, "\n%sdone: %s%s\n\n", colorDim, ev.StopReason, colorReset)
		} else {
			fmt.Println()
		}
	case agent.ErrorEvent:
		return ev.Err
	}
	return nil
}

func printWelcome() {
	fmt.Println(colorCyan + "MyCode CLI" + colorReset)
	fmt.Printf("%smodel: %s | /help for commands | /exit to quit%s\n\n", colorDim, defaultModelName, colorReset)
}

func promptLabel() string {
	return colorGreen + "you" + colorReset + colorDim + " > " + colorReset
}

func assistantLabel() string {
	return colorCyan + "assistant" + colorReset + colorDim + " > " + colorReset
}

func handleCommand(input string) (handled bool, quit bool) {
	switch strings.ToLower(input) {
	case "/exit", "/quit", "exit", "quit":
		return true, true
	case "/help":
		printHelp()
		return true, false
	case "/clear", "clear":
		fmt.Print("\033[H\033[2J")
		printWelcome()
		return true, false
	default:
		return false, false
	}
}

func printHelp() {
	fmt.Println()
	fmt.Println(colorCyan + "Commands" + colorReset)
	fmt.Println("  /help   show this help")
	fmt.Println("  /clear  clear the screen")
	fmt.Println("  /exit   quit MyCode")
	fmt.Println()
}

func printError(label string, err error) {
	if err == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "%s%s:%s %v\n", colorRed, label, colorReset, err)
}

func toolLine(action string, toolName string) string {
	if toolName == "" {
		toolName = "tool"
	}
	return fmt.Sprintf("tool %s: %s", action, toolName)
}

func dim(text string) string {
	return colorDim + text + colorReset
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
