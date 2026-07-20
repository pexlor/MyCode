package tool

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
)

const GlobDescription = `List files matching a glob pattern recursively.
Patterns may include ** to match across directories, for example **/*.go.`

type GlobTool struct{}

func (t *GlobTool) Name() string        { return "Glob" }
func (t *GlobTool) Description() string { return GlobDescription }
func (t *GlobTool) Schema() *ToolSchema {
	return &ToolSchema{Name: t.Name(), Description: t.Description(), Parameters: map[string]any{
		"type": "object", "properties": map[string]any{
			"pattern":     map[string]any{"type": "string", "description": "Glob pattern, for example **/*.go."},
			"path":        map[string]any{"type": "string", "description": "Directory to search.", "default": "."},
			"max_results": map[string]any{"type": "integer", "default": 500},
		}, "required": []string{"pattern"},
	}}
}

func (t *GlobTool) Execute(ctx context.Context, args map[string]any) ToolResult {
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return toolError("pattern is required and must be a string")
	}
	re, err := globRegex(filepath.ToSlash(pattern))
	if err != nil {
		return toolError(err.Error())
	}
	root, _ := args["path"].(string)
	if root == "" {
		root = "."
	}
	maxResults := globMaxResults(args)
	var files []string
	limitReached := errors.New("search result limit reached")
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if re.MatchString(filepath.ToSlash(rel)) {
			files = append(files, path)
			if len(files) >= maxResults {
				return limitReached
			}
		}
		return nil
	})
	if err != nil && err != limitReached {
		return toolError(fmt.Sprintf("list files: %v", err))
	}
	if len(files) == 0 {
		return ToolResult{Output: "No files found."}
	}
	output := strings.Join(files, "\n")
	if err == limitReached {
		output += "\nResults truncated at max_results."
	}
	return ToolResult{Output: output}
}

func globMaxResults(args map[string]any) int {
	max := intArg(args, "max_results", 500)
	if max < 1 {
		return 1
	}
	if max > 10000 {
		return 10000
	}
	return max
}

func globRegex(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(pattern[i])))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}
