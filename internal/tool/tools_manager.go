package tool

import (
	"MyCode/internal/permission"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ToolsManager struct {
	tools       map[string]Tool
	permissions permission.PermissionManager
}

func NewToolsManager() *ToolsManager {
	m := &ToolsManager{tools: make(map[string]Tool)}
	workspace, err := os.Getwd()
	if err == nil {
		m.permissions, _ = permission.NewManager(permission.DefaultPolicy(workspace))
	}
	return m
}

func (m *ToolsManager) RegisterTool(t Tool) {
	m.tools[t.Name()] = t
}

func (m *ToolsManager) GetTool(name string) Tool {
	return m.tools[name]
}

// SetPermissionManager replaces the mandatory permission gateway used by Execute.
// Passing nil restores default-deny behavior rather than disabling authorization.
func (m *ToolsManager) SetPermissionManager(manager permission.PermissionManager) {
	m.permissions = manager
}

func (m *ToolsManager) PermissionManager() permission.PermissionManager { return m.permissions }

// Execute is the only tool execution entry point used by the agent runtime.
func (m *ToolsManager) Execute(ctx context.Context, name string, args map[string]any) ToolResult {
	registered := m.GetTool(name)
	if registered == nil {
		return ToolResult{Output: fmt.Sprintf("tool %q is not registered", name), IsError: true}
	}
	if m.permissions == nil {
		return ToolResult{Output: "permission denied: permission manager is not configured", IsError: true}
	}
	req := buildPermissionRequest(name, args)
	result, err := m.permissions.Authorize(ctx, req)
	if err != nil {
		return ToolResult{Output: fmt.Sprintf("permission check failed: %v", err), IsError: true}
	}
	if result.Decision != permission.Allow {
		return ToolResult{Output: fmt.Sprintf("permission %s: %s", result.Decision, result.Reason), IsError: true}
	}
	return registered.Execute(ctx, args)
}

func (m *ToolsManager) BuildAllSchemas() []*ToolSchema {
	schemas := make([]*ToolSchema, 0, len(m.tools))
	for _, t := range m.tools {
		base := t.Schema()
		schemas = append(schemas, base)
	}
	return schemas
}

func CreateDefaultTools() *ToolsManager {
	toolsManager := NewToolsManager()
	toolsManager.RegisterTool(&ReadFileTool{})
	toolsManager.RegisterTool(NewBashTool())
	workspace, err := os.Getwd()
	if err == nil {
		policy := permission.DefaultPolicy(workspace)
		policyPath := filepath.Join(workspace, ".agent", "permission.yaml")
		if _, statErr := os.Stat(policyPath); statErr == nil {
			loaded, loadErr := permission.LoadPolicy(policyPath)
			if loadErr == nil {
				policy = loaded
			} else {
				// An invalid explicit policy must fail closed.
				policy.Tools = make(map[string]permission.ToolPolicy)
			}
		} else if os.IsNotExist(statErr) {
			policy.Tools["readfile"] = permission.ToolPolicy{
				Permission:     permission.Allow,
				ToolPermission: permission.ToolPermission{ReadOnly: true},
			}
			policy.Tools["bash"] = permission.ToolPolicy{
				Permission: permission.Allow,
				ToolPermission: permission.ToolPermission{
					CanWrite:  true,
					CanDelete: true,
				},
			}
		} else {
			// An unreadable explicit policy location also fails closed.
			policy.Tools = make(map[string]permission.ToolPolicy)
		}
		confirmer := &permission.TerminalConfirmer{In: os.Stdin, Out: os.Stderr}
		if manager, managerErr := permission.NewManager(policy, permission.WithConfirmer(confirmer)); managerErr == nil {
			toolsManager.SetPermissionManager(manager)
		}
	}
	// todo : 添加其他工具
	return toolsManager
}

func buildPermissionRequest(name string, args map[string]any) permission.PermissionRequest {
	request := permission.PermissionRequest{ToolName: name, Arguments: args}
	lowerName := strings.ToLower(name)
	switch {
	case strings.Contains(lowerName, "read"):
		request.Action = "read"
		request.RiskLevel = permission.Safe
	case strings.Contains(lowerName, "delete"), strings.Contains(lowerName, "remove"):
		request.Action = "delete"
		request.RiskLevel = permission.High
	default:
		request.Action = "write"
		request.RiskLevel = permission.Low
	}
	for key, value := range args {
		lowerKey := strings.ToLower(key)
		if lowerKey == "command" || lowerKey == "cmd" {
			request.Command, _ = value.(string)
		}
		if lowerKey == "working_directory" || lowerKey == "cwd" {
			request.WorkingDirectory, _ = value.(string)
		}
		if strings.Contains(lowerKey, "path") || strings.Contains(lowerKey, "file") || strings.Contains(lowerKey, "directory") {
			switch paths := value.(type) {
			case string:
				request.ResolvedPaths = append(request.ResolvedPaths, paths)
			case []string:
				request.ResolvedPaths = append(request.ResolvedPaths, paths...)
			case []any:
				for _, path := range paths {
					if stringPath, ok := path.(string); ok {
						request.ResolvedPaths = append(request.ResolvedPaths, stringPath)
					}
				}
			}
		}
	}
	return request
}
