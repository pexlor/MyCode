package repl

import (
	"MyCode/internal/agent"
	contextmanager "MyCode/internal/context"
	"MyCode/internal/llm"
	"MyCode/internal/prompt"
	"MyCode/internal/session"
	"MyCode/internal/tool"
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
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

	systemPrompt, err := prompt.BuildSystemPrompt()
	if err != nil {
		printError("消息初始化失败", err)
		return
	}
	runtime, err := initAgent()
	if err != nil {
		printError("agent 初始化失败", err)
		return
	}
	defer runtime.cleanup()

	sessions, err := session.NewService(runtime.store, runtime.workspace, systemPrompt)
	if err != nil {
		printError("会话初始化失败", err)
		return
	}
	registry, err := NewDefaultCommandRegistry()
	if err != nil {
		printError("命令初始化失败", err)
		return
	}
	runtime.runner.SetContextManager(runtime.contextManager, sessions.Current().ID)
	commandContext := &CommandContext{Sessions: sessions, In: reader, Out: os.Stdout, Registry: registry, Clear: func(out io.Writer) {
		fmt.Fprint(out, "\033[H\033[2J")
		printWelcomeTo(out)
	}, OnSessionChange: func(sessionID string) {
		runtime.runner.SetContextManager(runtime.contextManager, sessionID)
	}}

	for {
		fmt.Print(promptLabel())
		userInput, err := reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				fmt.Println(dim("bye."))
				return
			}
			printError("读取输入失败", err)
			return
		}

		userInput = strings.TrimSpace(userInput)
		if userInput == "" {
			continue
		}

		result := registry.Execute(context.Background(), commandContext, userInput)
		if result.Handled {
			if result.Err != nil {
				printError("命令失败", result.Err)
			}
			if result.Quit {
				fmt.Println(dim("bye."))
				return
			}
			continue
		}

		if err := sessions.AddUserMessage(context.Background(), userInput); err != nil {
			printError("保存消息失败", err)
			continue
		}
		fmt.Print(assistantLabel())
		failed := false
		for event := range runtime.runner.Run(sessions.Current().Messages) {
			if err := handleAgentEvent(event); err != nil {
				printError("执行失败", err)
				failed = true
				break
			}
		}
		if failed {
			continue
		}
	}
}

type agentRuntime struct {
	runner         *agent.Agent
	contextManager *contextmanager.ContextManager
	store          *contextmanager.FileConversationStore
	workspace      string
	cleanup        func()
}

func initAgent() (*agentRuntime, error) {
	// 模型凭据只从环境变量读取，禁止写入源码、配置文件或 Session 归档。
	apiKey := os.Getenv("MYCODE_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("MYCODE_API_KEY is required")
	}
	protocol := "openai-compat"
	baseURL := envOrDefault("MYCODE_BASE_URL", defaultBaseURL)
	modelName := envOrDefault("MYCODE_MODEL", defaultModelName)

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

	tools, cleanup, err := tool.CreateDefaultToolsWithMCP(ctx)
	if err != nil {
		return nil, err
	}

	runner, err := agent.NewAgent(ctx, client, tools)
	if err != nil {
		cleanup()
		return nil, err
	}
	workspace, err := os.Getwd()
	if err != nil {
		cleanup()
		return nil, err
	}
	store, err := contextmanager.NewFileConversationStore(filepath.Join(workspace, ".context", "sessions"))
	if err != nil {
		cleanup()
		return nil, err
	}
	// 独立摘要模型是可选配置。配置后优先使用它，以便单独控制摘要成本和速度；
	// 未配置或调用失败时，ContextManager 会回退当前对话模型一次。
	var primary contextmanager.Summarizer
	summaryModel := os.Getenv("MYCODE_SUMMARY_MODEL")
	if summaryModel != "" {
		summaryAPIKey := os.Getenv("MYCODE_SUMMARY_API_KEY")
		if summaryAPIKey == "" {
			cleanup()
			return nil, fmt.Errorf("MYCODE_SUMMARY_API_KEY is required when MYCODE_SUMMARY_MODEL is set")
		}
		summaryClient, summaryErr := llm.NewClient(&llm.ModelParm{
			Protocol:  protocol,
			BaseURL:   envOrDefault("MYCODE_SUMMARY_BASE_URL", baseURL),
			APIKey:    summaryAPIKey,
			ModelName: summaryModel,
		})
		if summaryErr != nil {
			cleanup()
			return nil, summaryErr
		}
		primary = contextmanager.LLMSummarizer{Client: summaryClient}
	}
	contextManager, err := contextmanager.NewContextManager(contextmanager.ContextManagerConfig{
		Store: store, Estimator: contextmanager.ConservativeEstimator{}, Policy: contextmanager.DefaultPolicy(),
		Model: contextmanager.ModelContextSpec{
			ModelName: modelName, ContextWindow: envInt("MYCODE_CONTEXT_WINDOW", 128000),
			MaxOutputTokens: envInt("MYCODE_MAX_OUTPUT_TOKENS", 8192),
		},
		Workspace: workspace,
		Primary:   primary,
		Fallback:  contextmanager.LLMSummarizer{Client: client},
	})
	if err != nil {
		cleanup()
		return nil, err
	}
	return &agentRuntime{runner: runner, contextManager: contextManager, store: store, workspace: workspace, cleanup: cleanup}, nil
}

func envOrDefault(name, fallback string) string {
	// 空白环境变量视为未配置，避免把空字符串传给 LLM Client。
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) int {
	// 非法或非正数配置使用安全默认值，保证预算计算始终拿到正整数。
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
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
		fmt.Fprintf(os.Stderr, "\n%stokens: input %d | output %d | total %d", colorDim, ev.Usage.InputTokens, ev.Usage.OutputTokens, ev.Usage.TotalTokens)
		if ev.Usage.CacheReadTokens > 0 {
			fmt.Fprintf(os.Stderr, " | cache read %d", ev.Usage.CacheReadTokens)
		}
		fmt.Fprint(os.Stderr, colorReset)
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
	printWelcomeTo(os.Stdout)
}

func printWelcomeTo(out io.Writer) {
	fmt.Fprintln(out, colorCyan+"MyCode CLI"+colorReset)
	fmt.Fprintf(out, "%smodel: %s | /help for commands | /exit to quit%s\n\n", colorDim, defaultModelName, colorReset)
}

func promptLabel() string {
	return colorGreen + "you" + colorReset + colorDim + " > " + colorReset
}

func assistantLabel() string {
	return colorCyan + "assistant" + colorReset + colorDim + " > " + colorReset
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
