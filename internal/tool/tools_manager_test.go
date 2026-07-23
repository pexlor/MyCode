package tool

import (
	"MyCode/internal/permission"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

type testTool struct{ called bool }

func (t *testTool) Name() string        { return "ReadTest" }
func (t *testTool) Description() string { return "test" }
func (t *testTool) Schema() *ToolSchema { return &ToolSchema{Name: t.Name()} }
func (t *testTool) Execute(context.Context, map[string]any) ToolResult {
	t.called = true
	return ToolResult{Output: "ok"}
}

func TestExecuteAlwaysUsesPermissionManager(t *testing.T) {
	workspace := t.TempDir()
	policy := permission.DefaultPolicy(workspace)
	policy.Tools["read_test"] = permission.ToolPolicy{Permission: permission.Allow, ToolPermission: permission.ToolPermission{ReadOnly: true}}
	manager, err := permission.NewManager(policy)
	if err != nil {
		t.Fatal(err)
	}

	registered := &testTool{}
	tools := NewToolsManager()
	tools.RegisterTool(registered)
	tools.SetPermissionManager(manager)

	result := tools.Execute(context.Background(), registered.Name(), map[string]any{"file_path": filepath.Join(workspace, "file.txt")})
	if result.IsError || !registered.called {
		t.Fatalf("allowed execution failed: %#v", result)
	}

	registered.called = false
	result = tools.Execute(context.Background(), registered.Name(), map[string]any{"file_path": filepath.Join(workspace, "..", "secret.txt")})
	if !result.IsError || registered.called {
		t.Fatalf("denied execution reached tool: %#v", result)
	}
}

func TestExecuteUsesResolvedFilePath(t *testing.T) {
	workspace := t.TempDir()
	policy := permission.DefaultPolicy(workspace)
	policy.Tools["read_test"] = permission.ToolPolicy{Permission: permission.Allow, ToolPermission: permission.ToolPermission{ReadOnly: true}}
	manager, err := permission.NewManager(policy)
	if err != nil {
		t.Fatal(err)
	}
	registered := &testTool{}
	tools := NewToolsManager()
	tools.RegisterTool(registered)
	tools.SetPermissionManager(manager)
	arguments := map[string]any{"file_path": "relative.txt"}
	result := tools.Execute(context.Background(), registered.Name(), arguments)
	if result.IsError {
		t.Fatalf("execution failed: %#v", result)
	}
	canonicalWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(canonicalWorkspace, "relative.txt")
	if arguments["file_path"] != want {
		t.Fatalf("resolved file_path = %#v, want %q", arguments["file_path"], want)
	}
}

func TestExecutePreservesPermissionDecisionWhenAuthorizationErrors(t *testing.T) {
	tools := NewToolsManager()
	tools.RegisterTool(&testTool{})
	tools.SetPermissionManager(permissionManagerFunc(func(context.Context, permission.PermissionRequest) (permission.PermissionResult, error) {
		return permission.PermissionResult{Decision: permission.Deny, Reason: "audit denied access"}, errors.New("audit sink unavailable")
	}))

	result := tools.Execute(context.Background(), "ReadTest", map[string]any{})
	if !result.IsError {
		t.Fatalf("result = %#v, want error", result)
	}
	for _, want := range []string{"permission deny: audit denied access", "permission check failed: audit sink unavailable"} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("result output = %q, missing %q", result.Output, want)
		}
	}
}

type permissionManagerFunc func(context.Context, permission.PermissionRequest) (permission.PermissionResult, error)

func (f permissionManagerFunc) Authorize(ctx context.Context, req permission.PermissionRequest) (permission.PermissionResult, error) {
	return f(ctx, req)
}

func TestBuildSchemasReturnsRequestedSubset(t *testing.T) {
	manager := NewToolsManager()
	manager.RegisterTool(&ReadFileTool{})
	manager.RegisterTool(&GrepTool{})
	manager.RegisterTool(&GlobTool{})

	schemas, err := manager.BuildSchemas([]string{"ReadFile", "Grep"})
	if err != nil {
		t.Fatal(err)
	}
	if len(schemas) != 2 || schemas[0].Name != "ReadFile" || schemas[1].Name != "Grep" {
		t.Fatalf("schemas = %#v", schemas)
	}
	if _, err := manager.BuildSchemas([]string{"Missing"}); err == nil {
		t.Fatal("expected unknown tool error")
	}
}
