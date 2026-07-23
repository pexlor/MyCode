package repl

import (
	"MyCode/internal/agent"
	appconfig "MyCode/internal/config"
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
	"strings"
)

const (
	colorReset = "\033[0m"
	colorDim   = "\033[2m"
	colorCyan  = "\033[36m"
	colorGreen = "\033[32m"
	colorRed   = "\033[31m"
	colorGray  = "\033[90m"
	colorWhite = "\033[97m"
	colorBold  = "\033[1m"
)

type lineInput interface {
	ReadLine(prompt string) (string, error)
	Close() error
}

type streamLineInput struct{ reader *bufio.Reader }

func newStreamLineInput(reader io.Reader) *streamLineInput {
	return &streamLineInput{reader: bufio.NewReader(reader)}
}

func (input *streamLineInput) ReadLine(_ string) (string, error) {
	line, err := input.reader.ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}

func (*streamLineInput) Close() error { return nil }

func openLineInput(registry *CommandRegistry) lineInput {
	info, err := os.Stdin.Stat()
	if err == nil && info.Mode()&os.ModeCharDevice != 0 {
		return newTerminalLineInput(registry)
	}
	return newStreamLineInput(os.Stdin)
}

func REPL() {
	// 交互模式
	runInteractive()
}

func runInteractive() {
	config, err := appconfig.Load(os.Stderr)
	if err != nil {
		printError("配置加载失败", err)
		return
	}
	reader := bufio.NewReader(os.Stdin)
	workspace, workspaceErr := os.Getwd()
	if workspaceErr != nil {
		workspace = "~"
	}

	printWelcomeTo(os.Stdout, config.Model.Name, workspace)

	systemPrompt, err := prompt.BuildSystemPrompt()
	if err != nil {
		printError("消息初始化失败", err)
		return
	}
	runtime, err := initAgent(config)
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
	input := openLineInput(registry)
	defer input.Close()
	runtime.runner.SetContextManager(runtime.contextManager, sessions.Current().ID)
	commandContext := &CommandContext{Sessions: sessions, In: reader, Out: os.Stdout, Registry: registry, Clear: func(out io.Writer) {
		fmt.Fprint(out, "\033[H\033[2J")
		printWelcomeTo(out, config.Model.Name, workspace)
	}, OnSessionChange: func(sessionID string) {
		runtime.runner.SetContextManager(runtime.contextManager, sessionID)
	}}

	for {
		userInput, err := input.ReadLine(promptLabel())
		if err != nil {
			if errors.Is(err, io.EOF) {
				fmt.Println(dim("bye."))
				return
			}
			if errors.Is(err, errPromptAborted) {
				fmt.Println(dim("  输入已取消"))
				continue
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

func initAgent(config appconfig.Config) (*agentRuntime, error) {
	// 构造llm客户端
	client, err := llm.NewClient(modelParameters(config.Model))

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
	if config.Summary.Model != "" {
		summaryClient, summaryErr := llm.NewClient(&llm.ModelParm{
			Protocol:  config.Model.Protocol,
			BaseURL:   config.Summary.BaseURL,
			APIKey:    config.Summary.APIKey,
			ModelName: config.Summary.Model,
			MaxToken:  int64(config.Model.MaxTokens),
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
			ModelName: config.Model.Name, ContextWindow: config.Context.Window,
			MaxOutputTokens: config.Context.OutputReserve,
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

func modelParameters(model appconfig.ModelConfig) *llm.ModelParm {
	return &llm.ModelParm{
		Protocol:  model.Protocol,
		BaseURL:   model.BaseURL,
		APIKey:    model.APIKey,
		ModelName: model.Name,
		MaxToken:  int64(model.MaxTokens),
	}
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

func printWelcomeTo(out io.Writer, modelName, workspace string) {
	modelName = firstNonEmpty(modelName, "not configured")
	fmt.Fprintln(out)
	fmt.Fprintf(out, "  %s›_%s  %s%sMyCode%s\n", colorCyan, colorReset, colorBold, colorWhite, colorReset)
	fmt.Fprintf(out, "  %s────────────────────────────────────────────────────────%s\n", colorGray, colorReset)
	fmt.Fprintf(out, "  %smodel: %s%s\n", colorGray, modelName, colorReset)
	fmt.Fprintf(out, "  %sdirectory: %s%s\n", colorGray, workspace, colorReset)
	fmt.Fprintf(out, "  %s/help for commands  ·  /exit to quit%s\n\n", colorDim, colorReset)
}

func promptLabel() string {
	// Keep the editable prompt free of ANSI bytes: line editors count prompt
	// columns, and escape sequences would shift the cursor during CJK input.
	return "› "
}

func assistantLabel() string {
	return "\n" + colorCyan + colorBold + "●" + colorReset + " "
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
