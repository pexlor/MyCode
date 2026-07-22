package repl

import (
	contextmanager "MyCode/internal/context"
	"MyCode/internal/session"
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

type Command struct {
	Name        string
	Usage       string
	Description string
	Run         func(context.Context, *CommandContext, string) CommandResult
}

type CommandRegistry struct {
	ordered []Command
	byName  map[string]Command
}

type CommandContext struct {
	Sessions        *session.Service
	In              *bufio.Reader
	Out             io.Writer
	Registry        *CommandRegistry
	Clear           func(io.Writer)
	OnSessionChange func(string)
}

type CommandResult struct {
	Handled bool
	Quit    bool
	Err     error
}

func NewCommandRegistry() *CommandRegistry {
	return &CommandRegistry{byName: make(map[string]Command)}
}

func (r *CommandRegistry) Register(command Command) error {
	command.Name = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(command.Name), "/"))
	if command.Name == "" || strings.ContainsAny(command.Name, " \t\r\n") || command.Run == nil {
		return errors.New("invalid command registration")
	}
	if _, exists := r.byName[command.Name]; exists {
		return fmt.Errorf("command %q already registered", command.Name)
	}
	r.byName[command.Name] = command
	r.ordered = append(r.ordered, command)
	return nil
}

func (r *CommandRegistry) Execute(ctx context.Context, commandContext *CommandContext, input string) CommandResult {
	input = strings.TrimSpace(input)
	if input == "" {
		return CommandResult{}
	}
	if strings.EqualFold(input, "exit") || strings.EqualFold(input, "quit") {
		input = "/exit"
	}
	if !strings.HasPrefix(input, "/") {
		return CommandResult{}
	}
	withoutSlash := strings.TrimPrefix(input, "/")
	name, arguments, _ := strings.Cut(withoutSlash, " ")
	name = strings.ToLower(strings.TrimSpace(name))
	arguments = strings.TrimSpace(arguments)
	command, exists := r.byName[name]
	if !exists {
		return CommandResult{Handled: true, Err: fmt.Errorf("unknown command /%s; use /help", name)}
	}
	result := command.Run(ctx, commandContext, arguments)
	result.Handled = true
	return result
}

func NewDefaultCommandRegistry() (*CommandRegistry, error) {
	registry := NewCommandRegistry()
	commands := []Command{
		helpCommand(),
		{Name: "new", Usage: "/new [标题]", Description: "创建并切换到新会话", Run: runNew},
		{Name: "sessions", Usage: "/sessions", Description: "列出最近会话", Run: runSessions},
		{Name: "resume", Usage: "/resume <id>", Description: "恢复并切换会话", Run: runResume},
		{Name: "delete", Usage: "/delete <id>", Description: "删除非当前会话", Run: runDelete},
		{Name: "rename", Usage: "/rename <标题>", Description: "重命名当前会话", Run: runRename},
		{Name: "current", Usage: "/current", Description: "显示当前会话", Run: runCurrent},
		{Name: "clear", Usage: "/clear", Description: "清理屏幕但保留上下文", Run: runClear},
		{Name: "exit", Usage: "/exit", Description: "退出 MyCode", Run: func(context.Context, *CommandContext, string) CommandResult { return CommandResult{Quit: true} }},
		{Name: "quit", Usage: "/quit", Description: "退出 MyCode", Run: func(context.Context, *CommandContext, string) CommandResult { return CommandResult{Quit: true} }},
	}
	for _, command := range commands {
		if err := registry.Register(command); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func helpCommand() Command {
	return Command{Name: "help", Usage: "/help [命令]", Description: "显示命令帮助", Run: func(_ context.Context, commandContext *CommandContext, arguments string) CommandResult {
		if commandContext == nil || commandContext.Registry == nil || commandContext.Out == nil {
			return CommandResult{Err: errors.New("help command is not initialized")}
		}
		if arguments != "" {
			name := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(arguments), "/"))
			command, exists := commandContext.Registry.byName[name]
			if !exists {
				return CommandResult{Err: fmt.Errorf("unknown command /%s", name)}
			}
			fmt.Fprintf(commandContext.Out, "%s\n  %s\n", command.Usage, command.Description)
			return CommandResult{}
		}
		fmt.Fprintln(commandContext.Out, "Commands")
		for _, command := range commandContext.Registry.ordered {
			fmt.Fprintf(commandContext.Out, "  %-20s %s\n", command.Usage, command.Description)
		}
		return CommandResult{}
	}}
}

func runNew(ctx context.Context, commandContext *CommandContext, arguments string) CommandResult {
	current, err := commandContext.Sessions.New(ctx, arguments)
	if err != nil {
		return CommandResult{Err: err}
	}
	notifySessionChange(commandContext, current.ID)
	fmt.Fprintf(commandContext.Out, "new session %s  %s\n", shortID(current.ID), current.Title)
	return CommandResult{}
}

func runSessions(ctx context.Context, commandContext *CommandContext, arguments string) CommandResult {
	if arguments != "" {
		return CommandResult{Err: errors.New("usage: /sessions")}
	}
	items, err := commandContext.Sessions.List(ctx, 20)
	if err != nil {
		return CommandResult{Err: err}
	}
	if len(items) == 0 {
		fmt.Fprintln(commandContext.Out, "no sessions")
		return CommandResult{}
	}
	currentID := commandContext.Sessions.Current().ID
	for _, item := range items {
		marker := " "
		if item.ID == currentID {
			marker = "*"
		}
		fmt.Fprintf(commandContext.Out, "%s %-8s  %-40s  %d messages  %s\n", marker, shortID(item.ID), truncateRunes(item.Title, 40), item.MessageCount, item.UpdatedAt.Local().Format("2006-01-02 15:04"))
	}
	return CommandResult{}
}

func runResume(ctx context.Context, commandContext *CommandContext, arguments string) CommandResult {
	if arguments == "" || strings.ContainsAny(arguments, " \t") {
		return CommandResult{Err: errors.New("usage: /resume <id>")}
	}
	current, err := commandContext.Sessions.Resume(ctx, arguments)
	if err != nil {
		return CommandResult{Err: err}
	}
	notifySessionChange(commandContext, current.ID)
	fmt.Fprintf(commandContext.Out, "resumed %s  %s  %d messages\n", shortID(current.ID), current.Title, len(current.Messages.History))
	return CommandResult{}
}

func runDelete(ctx context.Context, commandContext *CommandContext, arguments string) CommandResult {
	if arguments == "" || strings.ContainsAny(arguments, " \t") {
		return CommandResult{Err: errors.New("usage: /delete <id>")}
	}
	if commandContext.In == nil {
		return CommandResult{Err: errors.New("delete confirmation input is unavailable")}
	}
	fmt.Fprintf(commandContext.Out, "delete session %s? [y/N] ", arguments)
	answer, err := commandContext.In.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return CommandResult{Err: err}
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer != "y" && answer != "yes" {
		fmt.Fprintln(commandContext.Out, "cancelled")
		return CommandResult{}
	}
	if err := commandContext.Sessions.Delete(ctx, arguments); err != nil {
		return CommandResult{Err: err}
	}
	fmt.Fprintln(commandContext.Out, "session deleted")
	return CommandResult{}
}

func runRename(ctx context.Context, commandContext *CommandContext, arguments string) CommandResult {
	if err := commandContext.Sessions.Rename(ctx, arguments); err != nil {
		return CommandResult{Err: err}
	}
	fmt.Fprintf(commandContext.Out, "renamed to %s\n", commandContext.Sessions.Current().Title)
	return CommandResult{}
}

func runCurrent(_ context.Context, commandContext *CommandContext, arguments string) CommandResult {
	if arguments != "" {
		return CommandResult{Err: errors.New("usage: /current")}
	}
	current := commandContext.Sessions.Current()
	state := "not persisted"
	if current.Persisted {
		state = "persisted"
	}
	fmt.Fprintf(commandContext.Out, "id: %s\ntitle: %s\ncreated: %s\nupdated: %s\nmessages: %d\nstate: %s\n", current.ID, current.Title, formatTime(current.CreatedAt), formatTime(current.UpdatedAt), len(current.Messages.History), state)
	return CommandResult{}
}

func runClear(_ context.Context, commandContext *CommandContext, arguments string) CommandResult {
	if arguments != "" {
		return CommandResult{Err: errors.New("usage: /clear")}
	}
	if commandContext.Clear != nil {
		commandContext.Clear(commandContext.Out)
	}
	return CommandResult{}
}

func notifySessionChange(commandContext *CommandContext, sessionID string) {
	if commandContext.OnSessionChange != nil {
		commandContext.OnSessionChange(sessionID)
	}
}

func shortID(id string) string {
	return truncateRunes(strings.TrimPrefix(id, "session-"), 8)
}
func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return string(runes)
}
func formatTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.Local().Format(time.RFC3339)
}

var _ contextmanager.SessionStore = (*contextmanager.FileConversationStore)(nil)
