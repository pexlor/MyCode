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
	"os/signal"
	"path/filepath"
	"strings"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
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

	thinkingBoxContentWidth = 64
	thinkingBoxLineCount    = 4
	thinkingBoxHeight       = thinkingBoxLineCount + 2
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
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	runtime.runner.SetContextManager(runtime.contextManager, sessions.Current().ID)
	commandContext := &CommandContext{Sessions: sessions, In: reader, Out: os.Stdout, Registry: registry, Thinking: runtime.runner, Clear: func(out io.Writer) {
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
		renderer := newAgentEventRenderer(os.Stderr, os.Stdout)
		failed := false
		interrupted := false
		turnContext, cancelTurn := context.WithCancel(context.Background())
		events := runtime.runner.RunContext(turnContext, sessions.Current().Messages)
	eventLoop:
		for {
			select {
			case <-interrupts:
				cancelTurn()
				interrupted = true
				break eventLoop
			case event, ok := <-events:
				if !ok {
					break eventLoop
				}
				if err := renderer.render(event); err != nil {
					printError("执行失败", err)
					failed = true
					break eventLoop
				}
			}
		}
		cancelTurn()
		if interrupted {
			renderer.clearStatus()
			renderer.finishThinking()
			fmt.Fprintln(os.Stderr, dim("  对话已中断"))
			continue
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
		Protocol:       model.Protocol,
		BaseURL:        model.BaseURL,
		APIKey:         model.APIKey,
		ModelName:      model.Name,
		MaxToken:       int64(model.MaxTokens),
		EnableThinking: model.EnableThinking,
	}
}

func handleAgentEvent(event agent.AgentEvent) error {
	return newAgentEventRenderer(os.Stderr, os.Stdout).render(event)
}

// agentEventRenderer draws transient progress on the current terminal line.
// It clears that line as soon as output arrives, so conversation-state labels
// do not become part of the transcript.
type agentEventRenderer struct {
	statusOut        io.Writer
	textOut          io.Writer
	statusVisible    bool
	thinkingVisible  bool
	assistantStarted bool
	thinking         strings.Builder
	assistantText    strings.Builder
	markdownRenderer *glamour.TermRenderer
}

func newAgentEventRenderer(statusOut, textOut io.Writer) *agentEventRenderer {
	markdownRenderer, _ := glamour.NewTermRenderer(glamour.WithStandardStyle(styles.DarkStyle), glamour.WithWordWrap(80))
	return &agentEventRenderer{statusOut: statusOut, textOut: textOut, markdownRenderer: markdownRenderer}
}

func (renderer *agentEventRenderer) render(event agent.AgentEvent) error {
	switch ev := event.(type) {
	case agent.TextEvent:
		renderer.clearStatus()
		renderer.finishThinking()
		renderer.assistantText.WriteString(ev.Text)
	case agent.ThinkingStartEvent:
		renderer.showStatus(conversationStatus("正在思考"))
	case agent.ThinkingEvent:
		renderer.clearStatus()
		renderer.thinking.WriteString(ev.Text)
		renderer.renderThinkingBox()
	case agent.ToolExecutionStartEvent:
		renderer.finishThinking()
		renderer.renderAssistantMarkdown()
		renderer.showStatus(toolLine("正在调用", ev.ToolName))
	case agent.ToolResultEvent:
		renderer.clearStatus()
		renderer.finishThinking()
		status := "ok"
		color := colorGreen
		if ev.IsError {
			status = "error"
			color = colorRed
		}
		fmt.Fprintf(renderer.statusOut, "%s%s%s %s%s%s\n", colorDim, toolLine("完成", ev.ToolName), colorReset, color, status, colorReset)
	case agent.DoneEvent:
		renderer.clearStatus()
		renderer.finishThinking()
		renderer.renderAssistantMarkdown()
		fmt.Fprintf(renderer.statusOut, "\n%stokens: input %d | output %d | total %d", colorDim, ev.Usage.InputTokens, ev.Usage.OutputTokens, ev.Usage.TotalTokens)
		if ev.Usage.CacheReadTokens > 0 {
			fmt.Fprintf(renderer.statusOut, " | cache read %d", ev.Usage.CacheReadTokens)
		}
		fmt.Fprint(renderer.statusOut, colorReset)
		if ev.StopReason != "" {
			fmt.Fprintf(renderer.statusOut, "\n%sdone: %s%s\n\n", colorDim, ev.StopReason, colorReset)
		} else {
			fmt.Fprintln(renderer.statusOut)
		}
	case agent.ErrorEvent:
		return ev.Err
	}
	return nil
}

func (renderer *agentEventRenderer) renderAssistantMarkdown() {
	markdown := renderer.assistantText.String()
	if markdown == "" {
		return
	}
	renderer.assistantText.Reset()
	if !renderer.assistantStarted {
		fmt.Fprint(renderer.textOut, assistantLabel())
		renderer.assistantStarted = true
	}
	if renderer.markdownRenderer == nil {
		fmt.Fprint(renderer.textOut, markdown)
		return
	}
	rendered, err := renderer.markdownRenderer.Render(markdown)
	if err != nil {
		fmt.Fprint(renderer.textOut, markdown)
		return
	}
	fmt.Fprint(renderer.textOut, rendered)
}

func (renderer *agentEventRenderer) showStatus(status string) {
	renderer.clearStatus()
	fmt.Fprintf(renderer.statusOut, "\r\033[2K%s%s%s", colorDim, status, colorReset)
	renderer.statusVisible = true
}

func (renderer *agentEventRenderer) clearStatus() {
	if renderer.statusVisible {
		fmt.Fprint(renderer.statusOut, "\r\033[2K")
		renderer.statusVisible = false
	}
}

func (renderer *agentEventRenderer) finishThinking() {
	if renderer.thinkingVisible {
		renderer.thinkingVisible = false
	}
}

// renderThinkingBox redraws a fixed-height viewport in place. The box keeps
// the terminal transcript compact while the most recent reasoning lines remain
// visible during generation.
func (renderer *agentEventRenderer) renderThinkingBox() {
	if renderer.thinkingVisible {
		fmt.Fprintf(renderer.statusOut, "\033[%dA", thinkingBoxHeight)
	}
	lines := recentThinkingLines(renderer.thinking.String(), thinkingBoxContentWidth, thinkingBoxLineCount)
	titleFill := strings.Repeat("─", thinkingBoxContentWidth-lipgloss.Width(" 思考 "))
	fmt.Fprintf(renderer.statusOut, "\r\033[2K%s┌─ 思考 %s┐%s\n", colorGray, titleFill, colorReset)
	for _, line := range lines {
		padding := strings.Repeat(" ", thinkingBoxContentWidth-lipgloss.Width(line))
		fmt.Fprintf(renderer.statusOut, "\r\033[2K%s│%s%s│%s\n", colorGray, line, padding, colorReset)
	}
	fmt.Fprintf(renderer.statusOut, "\r\033[2K%s└%s┘%s\n", colorGray, strings.Repeat("─", thinkingBoxContentWidth), colorReset)
	renderer.thinkingVisible = true
}

func recentThinkingLines(text string, width, limit int) []string {
	lines := make([]string, 0, limit)
	for _, sourceLine := range strings.Split(text, "\n") {
		current := ""
		for _, runeValue := range sourceLine {
			candidate := current + string(runeValue)
			if lipgloss.Width(candidate) > width && current != "" {
				lines = append(lines, current)
				current = string(runeValue)
			} else {
				current = candidate
			}
		}
		lines = append(lines, current)
	}
	if len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	for len(lines) < limit {
		lines = append([]string{""}, lines...)
	}
	return lines
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
	return colorCyan + colorBold + "●" + colorReset + " "
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
	return fmt.Sprintf("  · 工具%s：%s", action, toolName)
}

func conversationStatus(status string) string {
	return "  · " + status + "…"
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
