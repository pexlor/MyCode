package tool

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const GrepDescription = `Search text files recursively with a regular expression.
Returns matching lines as path:line_number:content.`

type GrepTool struct{}

func (t *GrepTool) Name() string        { return "Grep" }
func (t *GrepTool) Description() string { return GrepDescription }
func (t *GrepTool) Schema() *ToolSchema {
	return &ToolSchema{Name: t.Name(), Description: t.Description(), Parameters: map[string]any{
		"type": "object", "properties": map[string]any{
			"pattern":     map[string]any{"type": "string", "description": "Go regular expression to search for."},
			"path":        map[string]any{"type": "string", "description": "File or directory to search.", "default": "."},
			"max_results": map[string]any{"type": "integer", "default": 200},
		}, "required": []string{"pattern"},
	}}
}

func (t *GrepTool) Execute(ctx context.Context, args map[string]any) ToolResult {
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return toolError("pattern is required and must be a string")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return toolError(fmt.Sprintf("invalid pattern: %v", err))
	}
	root, _ := args["path"].(string)
	if root == "" {
		root = "."
	}
	maxResults := grepMaxResults(args)
	var output strings.Builder
	matches := 0
	limitReached := errors.New("search result limit reached")
	searchFile := func(path string) error {
		file, openErr := os.Open(path)
		if openErr != nil {
			return openErr
		}
		defer file.Close()
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for lineNo := 1; scanner.Scan(); lineNo++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			line := scanner.Text()
			if strings.IndexByte(line, 0) >= 0 {
				return nil
			}
			if re.MatchString(line) {
				fmt.Fprintf(&output, "%s:%d:%s\n", path, lineNo, line)
				matches++
				if matches >= maxResults {
					return limitReached
				}
			}
		}
		return scanner.Err()
	}
	info, err := os.Stat(root)
	if err != nil {
		return toolError(err.Error())
	}
	if !info.IsDir() {
		err = searchFile(root)
	} else {
		err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "node_modules") {
				return filepath.SkipDir
			}
			if entry.IsDir() || !entry.Type().IsRegular() {
				return nil
			}
			return searchFile(path)
		})
	}
	if err != nil && err != limitReached {
		return toolError(fmt.Sprintf("search files: %v", err))
	}
	if matches == 0 {
		return ToolResult{Output: "No matches found."}
	}
	if err == limitReached {
		output.WriteString("Results truncated at max_results.\n")
	}
	return ToolResult{Output: strings.TrimSuffix(output.String(), "\n")}
}

func grepMaxResults(args map[string]any) int {
	max := intArg(args, "max_results", 200)
	if max < 1 {
		return 1
	}
	if max > 10000 {
		return 10000
	}
	return max
}
