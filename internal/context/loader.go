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

// DemandLoader 负责第 0 层按需加载，不调用 LLM，也不修改持久化会话。
type DemandLoader struct {
	Workspace string
}

// LoadRules 加载根规则以及活跃文件路径沿途的局部规则。
// 返回顺序固定为父目录到子目录，使更具体的规则出现在更靠后的位置。
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
			continue
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
	// 候选规则是从叶子路径向上发现的，这里按目录深度重排，保证父规则先于子规则。
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

// SelectTools 返回本轮所有可用工具 schema。
// 工具可见性不承担权限控制；所有调用仍由 ToolsManager 在执行阶段统一鉴权。
func (DemandLoader) SelectTools(_ string, _ []string, all []*tool.ToolSchema) []*tool.ToolSchema {
	return append([]*tool.ToolSchema(nil), all...)
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
