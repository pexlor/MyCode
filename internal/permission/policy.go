package permission

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type WorkspacePolicy struct {
	Root string `yaml:"root"`
}

type ToolPolicy struct {
	Permission     PermissionDecision `yaml:"permission"`
	ToolPermission `yaml:",inline"`
}

type Policy struct {
	Default        PermissionDecision    `yaml:"default"`
	Workspace      WorkspacePolicy       `yaml:"workspace"`
	Tools          map[string]ToolPolicy `yaml:"tools"`
	ProtectedPaths []string              `yaml:"protected_paths"`
}

func DefaultPolicy(workspace string) Policy {
	if strings.TrimSpace(workspace) == "" {
		workspace = "."
	}
	return Policy{
		Default:        Deny,
		Workspace:      WorkspacePolicy{Root: workspace},
		Tools:          make(map[string]ToolPolicy),
		ProtectedPaths: []string{"/", "/etc", "/boot", "/usr", "/var", "/proc", "/sys", "~/.ssh"},
	}
}

func LoadPolicy(path string) (Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Policy{}, fmt.Errorf("read permission policy: %w", err)
	}
	baseDir := filepath.Dir(path)
	if strings.EqualFold(filepath.Base(baseDir), ".agent") {
		baseDir = filepath.Dir(baseDir)
	}
	policy := DefaultPolicy(baseDir)
	if err := yaml.Unmarshal(data, &policy); err != nil {
		return Policy{}, fmt.Errorf("parse permission policy: %w", err)
	}
	if !filepath.IsAbs(policy.Workspace.Root) {
		policy.Workspace.Root = filepath.Join(baseDir, policy.Workspace.Root)
	}
	if err := policy.Validate(); err != nil {
		return Policy{}, err
	}
	return policy, nil
}

func (p *Policy) Validate() error {
	if p == nil {
		return errors.New("permission policy cannot be nil")
	}
	if p.Default == "" {
		p.Default = Deny
	}
	if !validDecision(p.Default) || p.Default == Confirm {
		return fmt.Errorf("invalid default permission %q", p.Default)
	}
	if strings.TrimSpace(p.Workspace.Root) == "" {
		return errors.New("workspace root is required")
	}
	abs, err := filepath.Abs(p.Workspace.Root)
	if err != nil {
		return fmt.Errorf("resolve workspace root: %w", err)
	}
	p.Workspace.Root = filepath.Clean(abs)
	if p.Tools == nil {
		p.Tools = make(map[string]ToolPolicy)
	}
	normalizedTools := make(map[string]ToolPolicy, len(p.Tools))
	for name, tool := range p.Tools {
		if strings.TrimSpace(name) == "" {
			return errors.New("tool policy name cannot be empty")
		}
		if tool.Permission == "" {
			tool.Permission = p.Default
		}
		if !validDecision(tool.Permission) {
			return fmt.Errorf("invalid permission %q for tool %q", tool.Permission, name)
		}
		normalizedTools[canonicalToolName(name)] = tool
	}
	p.Tools = normalizedTools
	return nil
}

func (p Policy) Tool(name string) (ToolPolicy, bool) {
	canonicalName := canonicalToolName(name)
	tool, ok := p.Tools[canonicalName]
	if !ok && canonicalName == "bash" {
		tool, ok = p.Tools["shell"]
	}
	return tool, ok
}

func canonicalToolName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	return strings.NewReplacer("_", "", "-", "", " ", "").Replace(name)
}

func validDecision(d PermissionDecision) bool { return d == Allow || d == Deny || d == Confirm }
