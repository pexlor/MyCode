package tool

import (
	"MyCode/internal/permission"
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBashToolSchema(t *testing.T) {
	tool := NewBashTool()
	schema := tool.Schema()
	if schema.Name != "Bash" {
		t.Fatalf("name = %q, want Bash", schema.Name)
	}
	required, ok := schema.Parameters["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "command" {
		t.Fatalf("required schema = %#v", schema.Parameters["required"])
	}
}

func TestBashToolExecute(t *testing.T) {
	tool := NewBashTool()
	if tool.executable == "" {
		t.Skip("no supported shell installed")
	}
	workingDirectory := t.TempDir()
	result := tool.Execute(context.Background(), map[string]any{
		"command":           "echo hello",
		"working_directory": workingDirectory,
		"timeout_ms":        float64(5_000),
	})
	if result.IsError {
		t.Fatalf("command failed: %s", result.Output)
	}
	if !strings.Contains(strings.ToLower(result.Output), "hello") {
		t.Fatalf("output = %q, want hello", result.Output)
	}
}

func TestBashToolExitCodeAndValidation(t *testing.T) {
	tool := NewBashTool()
	if tool.executable == "" {
		t.Skip("no supported shell installed")
	}
	result := tool.Execute(context.Background(), map[string]any{"command": "exit 7"})
	if !result.IsError || !strings.Contains(result.Output, "code 7") {
		t.Fatalf("result = %#v, want exit code error", result)
	}
	result = tool.Execute(context.Background(), map[string]any{"command": "echo test", "timeout_ms": 0})
	if !result.IsError || !strings.Contains(result.Output, "timeout_ms") {
		t.Fatalf("result = %#v, want timeout validation error", result)
	}
	result = tool.Execute(context.Background(), map[string]any{"command": "echo test", "working_directory": filepath.Join(t.TempDir(), "missing")})
	if !result.IsError || !strings.Contains(result.Output, "working_directory") {
		t.Fatalf("result = %#v, want directory validation error", result)
	}
}

func TestBashToolTimeoutAndOutputLimit(t *testing.T) {
	tool := NewBashTool()
	if tool.executable == "" {
		t.Skip("no supported shell installed")
	}
	sleepCommand := "sleep 2"
	outputCommand := "printf 1234567890"
	if runtime.GOOS == "windows" && !strings.Contains(strings.ToLower(filepath.Base(tool.executable)), "bash") {
		sleepCommand = "Start-Sleep -Seconds 2"
		outputCommand = "[Console]::Write('1234567890')"
	}
	result := tool.Execute(context.Background(), map[string]any{"command": sleepCommand, "timeout_ms": 50})
	if !result.IsError || !strings.Contains(result.Output, "timed out") {
		t.Fatalf("result = %#v, want timeout", result)
	}
	tool.maxOutputBytes = 5
	result = tool.Execute(context.Background(), map[string]any{"command": outputCommand})
	if result.IsError || !strings.Contains(result.Output, "output truncated") {
		t.Fatalf("result = %#v, want truncated output", result)
	}
}

func TestBashToolThroughPermissionManager(t *testing.T) {
	bash := NewBashTool()
	if bash.executable == "" {
		t.Skip("no supported shell installed")
	}
	workspace := t.TempDir()
	policy := permission.DefaultPolicy(workspace)
	policy.Tools["shell"] = permission.ToolPolicy{
		Permission: permission.Allow,
		ToolPermission: permission.ToolPermission{
			CanWrite:  true,
			CanDelete: true,
		},
	}
	manager, err := permission.NewManager(policy)
	if err != nil {
		t.Fatal(err)
	}
	tools := NewToolsManager()
	tools.RegisterTool(bash)
	tools.SetPermissionManager(manager)
	result := tools.Execute(context.Background(), "Bash", map[string]any{
		"command":           "pwd",
		"working_directory": ".",
	})
	if result.IsError {
		t.Fatalf("permission-wrapped Bash failed: %s", result.Output)
	}
	if !strings.Contains(strings.ToLower(result.Output), strings.ToLower(filepath.Base(workspace))) {
		t.Fatalf("output = %q, want workspace path", result.Output)
	}
}
