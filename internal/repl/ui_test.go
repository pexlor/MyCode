package repl

import (
	"MyCode/internal/agent"
	"MyCode/internal/config"
	"bytes"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestRenderAgentEventClearsProgressAndShowsThinkingContent(t *testing.T) {
	var statusOutput bytes.Buffer
	var textOutput bytes.Buffer
	renderer := newAgentEventRenderer(&statusOutput, &textOutput)
	for _, event := range []agent.AgentEvent{
		agent.ThinkingStartEvent{},
		agent.ThinkingEvent{Text: "private chain of thought"},
		agent.ThinkingEvent{Text: " continues"},
		agent.ToolExecutionStartEvent{ToolName: "ReadFile"},
		agent.ToolResultEvent{ToolName: "ReadFile"},
	} {
		if err := renderer.render(event); err != nil {
			t.Fatal(err)
		}
	}

	plain := stripANSI(statusOutput.String())
	for _, want := range []string{"正在思考", "private chain of thought", "工具正在调用：ReadFile", "工具完成：ReadFile", "ok"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("progress output %q does not contain %q", plain, want)
		}
	}
	if !strings.Contains(statusOutput.String(), "\r\033[2K") {
		t.Fatalf("progress status was not cleared: %q", statusOutput.String())
	}
	if !strings.Contains(plain, "┌─ 思考") || !strings.Contains(statusOutput.String(), "\033[6A") {
		t.Fatalf("thinking was not redrawn in a fixed-height box: %q", statusOutput.String())
	}
}

func TestRenderAgentEventRendersAssistantMarkdownAfterCompletion(t *testing.T) {
	var statusOutput bytes.Buffer
	var textOutput bytes.Buffer
	renderer := newAgentEventRenderer(&statusOutput, &textOutput)
	if err := renderer.render(agent.TextEvent{Text: "# 标题\n\n- 一项\n- `代码`"}); err != nil {
		t.Fatal(err)
	}
	if textOutput.Len() != 0 {
		t.Fatalf("streaming text was rendered before Markdown was complete: %q", textOutput.String())
	}
	if err := renderer.render(agent.DoneEvent{}); err != nil {
		t.Fatal(err)
	}
	plain := stripANSI(textOutput.String())
	for _, want := range []string{"标题", "一项", "代码"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("rendered Markdown %q does not contain %q", plain, want)
		}
	}
	if strings.Contains(plain, "# 标题") {
		t.Fatalf("heading marker was not rendered: %q", plain)
	}
}

func TestRecentThinkingLinesKeepsOnlyLatestWrappedLines(t *testing.T) {
	lines := recentThinkingLines("alpha\nbeta\ngamma", 5, 2)
	if got, want := strings.Join(lines, "|"), "beta|gamma"; got != want {
		t.Fatalf("lines = %q, want %q", got, want)
	}
}

func TestModelParametersFromConfig(t *testing.T) {
	model := config.ModelConfig{
		Protocol:       "anthropic",
		BaseURL:        "https://api.example.com",
		APIKey:         "secret",
		Name:           "model-a",
		MaxTokens:      4096,
		EnableThinking: true,
	}

	got := modelParameters(model)
	if got.Protocol != "anthropic" || got.BaseURL != "https://api.example.com" || got.APIKey != "secret" {
		t.Fatalf("connection parameters = %#v", got)
	}
	if got.ModelName != "model-a" || got.MaxToken != 4096 || !got.EnableThinking {
		t.Fatalf("model parameters = %#v", got)
	}
}

func TestPrintWelcomeUsesConfiguredModel(t *testing.T) {
	var output bytes.Buffer
	printWelcomeTo(&output, "model-a", "/repo")

	if !strings.Contains(output.String(), "model: model-a") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestStreamLineInputPreservesChineseText(t *testing.T) {
	input := newStreamLineInput(strings.NewReader("帮我修复中文输入\n"))

	got, err := input.ReadLine("")
	if err != nil {
		t.Fatal(err)
	}
	if got != "帮我修复中文输入" {
		t.Fatalf("ReadLine() = %q", got)
	}
}

func TestWelcomeUsesCodexStyleInformationCard(t *testing.T) {
	var output bytes.Buffer
	printWelcomeTo(&output, "gpt-5.6-sol", "/Users/test/project")

	plain := stripANSI(output.String())
	for _, want := range []string{"MyCode", "model:", "gpt-5.6-sol", "directory:", "/Users/test/project", "/help"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("welcome output %q does not contain %q", plain, want)
		}
	}
}

func TestCommandHintsAppearAndFilterWhileTypingSlashCommand(t *testing.T) {
	registry, err := NewDefaultCommandRegistry()
	if err != nil {
		t.Fatal(err)
	}

	all := commandHints(registry, "/")
	if len(all) != len(registry.ordered) {
		t.Fatalf("commandHints(/) returned %d hints, want %d", len(all), len(registry.ordered))
	}

	filtered := commandHints(registry, "/re")
	if len(filtered) != 2 || filtered[0].Name != "resume" || filtered[1].Name != "rename" {
		t.Fatalf("commandHints(/re) = %#v", filtered)
	}

	if got := commandHints(registry, "请帮我 /rename"); len(got) != 0 {
		t.Fatalf("ordinary Chinese input produced hints: %#v", got)
	}
	if got := commandHints(registry, "/rename 新标题"); len(got) != 0 {
		t.Fatalf("command arguments produced hints: %#v", got)
	}
}

func TestPromptModelSelectsAndCompletesCommandHint(t *testing.T) {
	registry, err := NewDefaultCommandRegistry()
	if err != nil {
		t.Fatal(err)
	}
	model := newPromptModel("› ", registry)
	updated, _ := model.Update(keyText("/re"))
	model = updated.(promptModel)
	if len(model.hints) != 2 || !strings.Contains(stripANSI(model.View().Content), "/resume") {
		t.Fatalf("typing /re did not show filtered hints: %q", model.View().Content)
	}

	updated, _ = model.Update(keySpecial(tea.KeyDown))
	model = updated.(promptModel)
	if model.selected != 1 {
		t.Fatalf("selected = %d, want 1", model.selected)
	}

	updated, _ = model.Update(keySpecial(tea.KeyTab))
	model = updated.(promptModel)
	if got := model.input.Value(); got != "/rename" {
		t.Fatalf("completed input = %q, want /rename", got)
	}
}

func TestPromptModelBrowsesHistoryAndRestoresDraft(t *testing.T) {
	model := newPromptModel("› ", nil, []string{"first input", "second input"})
	model.input.SetValue("draft")
	model.input.CursorEnd()

	updated, _ := model.Update(keySpecial(tea.KeyUp))
	model = updated.(promptModel)
	if got := model.input.Value(); got != "second input" {
		t.Fatalf("first up = %q", got)
	}
	updated, _ = model.Update(keySpecial(tea.KeyUp))
	model = updated.(promptModel)
	if got := model.input.Value(); got != "first input" {
		t.Fatalf("second up = %q", got)
	}
	updated, _ = model.Update(keySpecial(tea.KeyDown))
	model = updated.(promptModel)
	if got := model.input.Value(); got != "second input" {
		t.Fatalf("first down = %q", got)
	}
	updated, _ = model.Update(keySpecial(tea.KeyDown))
	model = updated.(promptModel)
	if got := model.input.Value(); got != "draft" {
		t.Fatalf("second down did not restore draft: %q", got)
	}
}

func TestPromptModelUsesRealTerminalCursorForIME(t *testing.T) {
	model := newPromptModel("› ", nil)
	if model.input.VirtualCursor() {
		t.Fatal("prompt input uses a virtual cursor")
	}
	if model.View().Cursor == nil {
		t.Fatal("prompt view does not expose a real terminal cursor")
	}
	model.input.SetValue("你好a")
	model.input.CursorEnd()
	model.syncInputViewport()
	if got, want := model.View().Cursor.Position.X, lipgloss.Width("› 你好a"); got != want {
		t.Fatalf("CJK cursor column = %d, want display width %d", got, want)
	}
}

func TestTerminalLineInputKeepsRecentDistinctHistory(t *testing.T) {
	input := newTerminalLineInput(nil)
	input.remember("first")
	input.remember("first")
	input.remember(" second ")
	input.remember(" ")
	if got, want := strings.Join(input.history, "|"), "first|second"; got != want {
		t.Fatalf("history = %q, want %q", got, want)
	}
}

func TestEnterKeepsParameterizedCommandHintEditable(t *testing.T) {
	registry, err := NewDefaultCommandRegistry()
	if err != nil {
		t.Fatal(err)
	}
	model := newPromptModel("› ", registry)
	updated, _ := model.Update(keyText("/re"))
	model = updated.(promptModel)
	updated, _ = model.Update(keySpecial(tea.KeyDown))
	model = updated.(promptModel)

	updated, command := model.Update(keySpecial(tea.KeyEnter))
	model = updated.(promptModel)
	if model.submitted || command != nil {
		t.Fatal("Enter submitted a command that still needs arguments")
	}
	if got := model.input.Value(); got != "/rename " {
		t.Fatalf("input = %q, want editable selected command /rename with trailing space", got)
	}
}

func TestEnterSubmitsParameterlessCommandHint(t *testing.T) {
	registry, err := NewDefaultCommandRegistry()
	if err != nil {
		t.Fatal(err)
	}
	model := newPromptModel("› ", registry)
	updated, _ := model.Update(keyText("/cu"))
	model = updated.(promptModel)
	updated, command := model.Update(keySpecial(tea.KeyEnter))
	model = updated.(promptModel)
	if !model.submitted || command == nil || model.input.Value() != "/current" {
		t.Fatalf("parameterless command was not submitted: %#v", model)
	}
}

func keyText(text string) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: tea.KeyExtended, Text: text})
}

func keySpecial(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: code})
}

func stripANSI(value string) string {
	var result strings.Builder
	for index := 0; index < len(value); {
		if value[index] == '\x1b' && index+1 < len(value) && value[index+1] == '[' {
			index += 2
			for index < len(value) && (value[index] < '@' || value[index] > '~') {
				index++
			}
			if index < len(value) {
				index++
			}
			continue
		}
		result.WriteByte(value[index])
		index++
	}
	return result.String()
}
