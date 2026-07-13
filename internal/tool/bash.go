package tool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	defaultBashTimeoutMS = 120_000
	maxBashTimeoutMS     = 600_000
	defaultMaxOutput     = 1 << 20 // 1 MiB
)

const BashDescription = `Execute a shell command in the workspace and return its combined output.
Uses Bash when available and PowerShell as the Windows fallback.
Commands are checked by the permission system before execution. Read-only and ordinary workspace operations may run automatically, dangerous operations require confirmation, and critical system operations are denied.
Use working_directory instead of changing directories inside the command. The default timeout is 120000 ms.`

type BashTool struct {
	executable     string
	commandPrefix  []string
	defaultTimeout time.Duration
	maxOutputBytes int
}

func NewBashTool() *BashTool {
	executable, prefix := detectShell()
	return &BashTool{
		executable:     executable,
		commandPrefix:  prefix,
		defaultTimeout: defaultBashTimeoutMS * time.Millisecond,
		maxOutputBytes: defaultMaxOutput,
	}
}

func (t *BashTool) Name() string        { return "Bash" }
func (t *BashTool) Description() string { return BashDescription }

func (t *BashTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "Shell command to execute",
				},
				"working_directory": map[string]any{
					"type":        "string",
					"description": "Directory in which to execute the command; defaults to the workspace",
				},
				"timeout_ms": map[string]any{
					"type":        "integer",
					"description": "Timeout in milliseconds (1-600000)",
					"default":     defaultBashTimeoutMS,
					"minimum":     1,
					"maximum":     maxBashTimeoutMS,
				},
			},
			"required":             []string{"command"},
			"additionalProperties": false,
		},
	}
}

func (t *BashTool) Execute(ctx context.Context, args map[string]any) ToolResult {
	if ctx == nil {
		ctx = context.Background()
	}
	command, _ := args["command"].(string)
	if strings.TrimSpace(command) == "" {
		return toolError("command is required")
	}
	workingDirectory, err := bashWorkingDirectory(args)
	if err != nil {
		return toolError(err.Error())
	}
	timeout, err := t.timeout(args)
	if err != nil {
		return toolError(err.Error())
	}
	executable, prefix := t.shell()
	if executable == "" {
		return toolError("no supported shell found (install bash or configure PowerShell on Windows)")
	}

	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	commandArgs := append(append([]string(nil), prefix...), command)
	cmd := exec.CommandContext(commandCtx, executable, commandArgs...)
	cmd.Dir = workingDirectory
	cmd.Env = os.Environ()

	limit := t.maxOutputBytes
	if limit <= 0 {
		limit = defaultMaxOutput
	}
	output := &limitedBuffer{limit: limit}
	cmd.Stdout = output
	cmd.Stderr = output
	err = cmd.Run()
	text := output.String()
	if output.Truncated() {
		text = strings.TrimRight(text, "\r\n") + fmt.Sprintf("\n[output truncated after %d bytes]", limit)
	}
	if commandCtx.Err() != nil {
		message := fmt.Sprintf("command timed out after %s", timeout)
		if errors.Is(commandCtx.Err(), context.Canceled) && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			message = "command canceled"
		}
		return ToolResult{Output: joinCommandOutput(text, message), IsError: true}
	}
	if err != nil {
		return ToolResult{Output: joinCommandOutput(text, formatCommandError(err)), IsError: true}
	}
	return ToolResult{Output: text}
}

func (t *BashTool) shell() (string, []string) {
	if t != nil && t.executable != "" {
		return t.executable, append([]string(nil), t.commandPrefix...)
	}
	return detectShell()
}

func (t *BashTool) timeout(args map[string]any) (time.Duration, error) {
	defaultTimeout := defaultBashTimeoutMS * time.Millisecond
	if t != nil && t.defaultTimeout > 0 {
		defaultTimeout = t.defaultTimeout
	}
	value, ok := args["timeout_ms"]
	if !ok {
		return defaultTimeout, nil
	}
	milliseconds, ok := numericInt(value)
	if !ok || milliseconds < 1 || milliseconds > maxBashTimeoutMS {
		return 0, fmt.Errorf("timeout_ms must be an integer between 1 and %d", maxBashTimeoutMS)
	}
	return time.Duration(milliseconds) * time.Millisecond, nil
}

func bashWorkingDirectory(args map[string]any) (string, error) {
	workingDirectory, _ := args["working_directory"].(string)
	if strings.TrimSpace(workingDirectory) == "" {
		var err error
		workingDirectory, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get working directory: %w", err)
		}
	}
	abs, err := filepath.Abs(workingDirectory)
	if err != nil {
		return "", fmt.Errorf("resolve working_directory: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("invalid working_directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("working_directory is not a directory: %s", abs)
	}
	return abs, nil
}

func detectShell() (string, []string) {
	if configured := strings.TrimSpace(os.Getenv("MYCODE_BASH")); configured != "" {
		return configured, []string{"-lc"}
	}
	if executable, err := exec.LookPath("bash"); err == nil {
		return executable, []string{"-lc"}
	}
	if runtime.GOOS == "windows" {
		if executable, err := exec.LookPath("pwsh"); err == nil {
			return executable, []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command"}
		}
		if executable, err := exec.LookPath("powershell"); err == nil {
			return executable, []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command"}
		}
	}
	return "", nil
}

func numericInt(value any) (int, bool) {
	switch number := value.(type) {
	case int:
		return number, true
	case int32:
		return int(number), true
	case int64:
		return int(number), true
	case float64:
		if number != float64(int(number)) {
			return 0, false
		}
		return int(number), true
	default:
		return 0, false
	}
}

func formatCommandError(err error) string {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return fmt.Sprintf("command exited with code %d", exitErr.ExitCode())
	}
	return fmt.Sprintf("execute command: %v", err)
}

func joinCommandOutput(output, message string) string {
	output = strings.TrimRight(output, "\r\n")
	if output == "" {
		return message
	}
	return output + "\n" + message
}

func toolError(message string) ToolResult {
	return ToolResult{Output: "Error: " + message, IsError: true}
}

type limitedBuffer struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	originalLength := len(data)
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		b.truncated = true
		return originalLength, nil
	}
	if len(data) > remaining {
		data = data[:remaining]
		b.truncated = true
	}
	_, _ = b.buffer.Write(data)
	return originalLength, nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func (b *limitedBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}
