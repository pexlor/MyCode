package repl

import (
	"MyCode/internal/config"
	"bytes"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestModelParametersFromConfig(t *testing.T) {
	model := config.ModelConfig{
		Protocol:  "anthropic",
		BaseURL:   "https://api.example.com",
		APIKey:    "secret",
		Name:      "model-a",
		MaxTokens: 4096,
	}

	got := modelParameters(model)
	if got.Protocol != "anthropic" || got.BaseURL != "https://api.example.com" || got.APIKey != "secret" {
		t.Fatalf("connection parameters = %#v", got)
	}
	if got.ModelName != "model-a" || got.MaxToken != 4096 {
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
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/re")})
	model = updated.(promptModel)
	if len(model.hints) != 2 || !strings.Contains(stripANSI(model.View()), "/resume") {
		t.Fatalf("typing /re did not show filtered hints: %q", model.View())
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(promptModel)
	if model.selected != 1 {
		t.Fatalf("selected = %d, want 1", model.selected)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(promptModel)
	if got := model.input.Value(); got != "/rename" {
		t.Fatalf("completed input = %q, want /rename", got)
	}
}

func TestEnterSubmitsSelectedCommandHint(t *testing.T) {
	registry, err := NewDefaultCommandRegistry()
	if err != nil {
		t.Fatal(err)
	}
	model := newPromptModel("› ", registry)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/re")})
	model = updated.(promptModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(promptModel)

	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(promptModel)
	if !model.submitted || command == nil {
		t.Fatal("Enter did not submit the selected hint")
	}
	if got := model.input.Value(); got != "/rename" {
		t.Fatalf("submitted input = %q, want selected command /rename", got)
	}
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
