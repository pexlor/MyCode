package contextmanager

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"MyCode/internal/tool"
)

type LoadedRule struct {
	Path        string
	Content     string
	ContentHash string
}

type DemandLoader struct {
	Workspace string
}

func (l DemandLoader) LoadRules(activePaths []string) ([]LoadedRule, error) {
	if l.Workspace == "" {
		return nil, nil
	}
	workspace, err := filepath.Abs(l.Workspace)
	if err != nil {
		return nil, err
	}
	candidates := []string{filepath.Join(workspace, ".agent", "context.md")}
	seen := map[string]bool{candidates[0]: true}
	for _, activePath := range activePaths {
		absolute := activePath
		if !filepath.IsAbs(absolute) {
			absolute = filepath.Join(workspace, absolute)
		}
		absolute = filepath.Clean(absolute)
		relative, err := filepath.Rel(workspace, absolute)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, errors.New("active path is outside workspace")
		}
		directory := filepath.Dir(absolute)
		for {
			candidate := filepath.Join(directory, ".agent", "context.md")
			if !seen[candidate] {
				candidates = append(candidates, candidate)
				seen[candidate] = true
			}
			if directory == workspace {
				break
			}
			parent := filepath.Dir(directory)
			if parent == directory {
				break
			}
			directory = parent
		}
	}
	// Candidates discovered from leaves are reordered by path depth so parent
	// rules always precede child rules.
	sortPathsByDepth(candidates)
	var rules []LoadedRule
	for _, candidate := range candidates {
		content, err := os.ReadFile(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(content)
		rules = append(rules, LoadedRule{Path: candidate, Content: string(content), ContentHash: hex.EncodeToString(digest[:])})
	}
	return rules, nil
}

func (DemandLoader) SelectTools(request string, activeToolNames []string, all []*tool.ToolSchema) []*tool.ToolSchema {
	lower := strings.ToLower(request)
	if strings.TrimSpace(lower) == "" {
		return append([]*tool.ToolSchema(nil), all...)
	}
	selected := make(map[string]bool)
	for _, name := range []string{"ReadFile", "Grep", "Glob"} {
		selected[name] = true
	}
	for _, name := range activeToolNames {
		selected[name] = true
	}
	if containsAny(lower, "edit", "write", "modify", "fix", "refactor", "修改", "写入", "实现", "修复", "重构") {
		selected["WriteFile"] = true
		selected["EditFile"] = true
	}
	if containsAny(lower, "run", "test", "build", "command", "执行", "运行", "测试", "构建", "命令") {
		selected["Bash"] = true
	}
	var result []*tool.ToolSchema
	for _, schema := range all {
		if schema == nil {
			continue
		}
		nameLower := strings.ToLower(schema.Name)
		if selected[schema.Name] || (strings.HasPrefix(nameLower, "mcp") && strings.Contains(lower, nameLower)) {
			result = append(result, schema)
		}
	}
	return result
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func sortPathsByDepth(paths []string) {
	for i := 1; i < len(paths); i++ {
		for j := i; j > 0 && pathDepth(paths[j]) < pathDepth(paths[j-1]); j-- {
			paths[j], paths[j-1] = paths[j-1], paths[j]
		}
	}
}

func pathDepth(path string) int {
	return strings.Count(filepath.Clean(path), string(filepath.Separator))
}
