package repl

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var errPromptAborted = errors.New("prompt aborted")

type commandHint struct {
	Name        string
	Usage       string
	Description string
}

func commandHints(registry *CommandRegistry, value string) []commandHint {
	if registry == nil || !strings.HasPrefix(value, "/") || strings.ContainsAny(value, " \t\r\n") {
		return nil
	}
	prefix := strings.ToLower(strings.TrimPrefix(value, "/"))
	hints := make([]commandHint, 0, len(registry.ordered))
	for _, command := range registry.ordered {
		if strings.HasPrefix(command.Name, prefix) {
			hints = append(hints, commandHint{
				Name:        command.Name,
				Usage:       command.Usage,
				Description: command.Description,
			})
		}
	}
	return hints
}

type terminalLineInput struct {
	registry *CommandRegistry
}

func newTerminalLineInput(registry *CommandRegistry) *terminalLineInput {
	return &terminalLineInput{registry: registry}
}

func (input *terminalLineInput) ReadLine(prompt string) (string, error) {
	model := newPromptModel(prompt, input.registry)
	program := tea.NewProgram(model, tea.WithInput(os.Stdin), tea.WithOutput(os.Stdout))
	result, err := program.Run()
	if err != nil {
		return "", err
	}
	finalModel, ok := result.(promptModel)
	if !ok {
		return "", errors.New("unexpected prompt state")
	}
	if finalModel.aborted {
		return "", errPromptAborted
	}
	return finalModel.input.Value(), nil
}

func (*terminalLineInput) Close() error { return nil }

type promptModel struct {
	input     textinput.Model
	registry  *CommandRegistry
	hints     []commandHint
	selected  int
	dismissed bool
	submitted bool
	aborted   bool
	width     int
}

func newPromptModel(prompt string, registry *CommandRegistry) promptModel {
	input := textinput.New()
	input.Prompt = prompt
	input.PromptStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	input.TextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	input.Cursor.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
	input.Focus()
	return promptModel{input: input, registry: registry, width: 100}
}

func (model promptModel) Init() tea.Cmd { return textinput.Blink }

func (model promptModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := message.(tea.WindowSizeMsg); ok {
		model.width = size.Width
		model.input.Width = max(12, size.Width-lipgloss.Width(model.input.Prompt)-1)
	}

	if key, ok := message.(tea.KeyMsg); ok {
		switch key.String() {
		case "ctrl+c":
			model.aborted = true
			return model, tea.Quit
		case "enter":
			if len(model.hints) > 0 {
				model.input.SetValue("/" + model.hints[model.selected].Name)
				model.input.CursorEnd()
			}
			model.submitted = true
			return model, tea.Quit
		case "esc":
			model.dismissed = true
			model.hints = nil
			return model, nil
		case "up":
			if len(model.hints) > 0 {
				model.selected = (model.selected - 1 + len(model.hints)) % len(model.hints)
				return model, nil
			}
		case "down":
			if len(model.hints) > 0 {
				model.selected = (model.selected + 1) % len(model.hints)
				return model, nil
			}
		case "tab":
			if len(model.hints) > 0 {
				model.input.SetValue("/" + model.hints[model.selected].Name)
				model.input.CursorEnd()
				model.dismissed = false
				model.refreshHints()
				return model, nil
			}
		}
	}

	previous := model.input.Value()
	var command tea.Cmd
	model.input, command = model.input.Update(message)
	if model.input.Value() != previous {
		model.dismissed = false
		model.selected = 0
		model.refreshHints()
	}
	return model, command
}

func (model *promptModel) refreshHints() {
	if model.dismissed {
		model.hints = nil
		return
	}
	model.hints = commandHints(model.registry, model.input.Value())
	if model.selected >= len(model.hints) {
		model.selected = 0
	}
}

func (model promptModel) View() string {
	if model.aborted {
		return ""
	}
	if model.submitted {
		return model.input.Prompt + model.input.Value() + "\n"
	}

	var view strings.Builder
	view.WriteString(model.input.View())
	if len(model.hints) == 0 {
		return view.String()
	}
	view.WriteByte('\n')
	limit := min(8, len(model.hints))
	for index, hint := range model.hints[:limit] {
		marker := "  "
		style := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
		if index == model.selected {
			marker = "› "
			style = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14"))
		}
		line := fmt.Sprintf("%s%-18s %s", marker, hint.Usage, hint.Description)
		if model.width > 0 && lipgloss.Width(line) > model.width {
			line = lipgloss.NewStyle().MaxWidth(model.width).Render(line)
		}
		view.WriteString(style.Render(line))
		if index < limit-1 {
			view.WriteByte('\n')
		}
	}
	if len(model.hints) > limit {
		fmt.Fprintf(&view, "\n%s还有 %d 个命令%s", colorDim, len(model.hints)-limit, colorReset)
	}
	view.WriteString("\n")
	view.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("  ↑↓ 选择 · Enter 确认 · Tab 补全 · Esc 收起"))
	return view.String()
}

var _ lineInput = (*terminalLineInput)(nil)
var _ io.Closer = (*terminalLineInput)(nil)
