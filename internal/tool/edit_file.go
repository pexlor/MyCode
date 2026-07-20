package tool

import (
	"context"
	"fmt"
	"os"
	"strings"
)

const EditFileDescription = `Make an exact text replacement in a file.
By default old_string must occur exactly once. Set replace_all to true to replace every occurrence.`

type EditFileTool struct{}

func (t *EditFileTool) Name() string        { return "EditFile" }
func (t *EditFileTool) Description() string { return EditFileDescription }
func (t *EditFileTool) Schema() *ToolSchema {
	return &ToolSchema{Name: t.Name(), Description: t.Description(), Parameters: map[string]any{
		"type": "object", "properties": map[string]any{
			"file_path":   map[string]any{"type": "string"},
			"old_string":  map[string]any{"type": "string", "description": "Exact text to replace."},
			"new_string":  map[string]any{"type": "string", "description": "Replacement text."},
			"replace_all": map[string]any{"type": "boolean", "default": false},
		}, "required": []string{"file_path", "old_string", "new_string"},
	}}
}

func (t *EditFileTool) Execute(ctx context.Context, args map[string]any) ToolResult {
	path, _ := args["file_path"].(string)
	if path == "" {
		return toolError("file_path is required and must be a string")
	}
	oldText, _ := args["old_string"].(string)
	if oldText == "" {
		return toolError("old_string is required and must not be empty")
	}
	newText, ok := args["new_string"].(string)
	if !ok {
		return toolError("new_string is required and must be a string")
	}
	if err := ctx.Err(); err != nil {
		return toolError(err.Error())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return toolError(fmt.Sprintf("read file: %v", err))
	}
	count := strings.Count(string(data), oldText)
	if count == 0 {
		return toolError("old_string was not found")
	}
	replaceAll, _ := args["replace_all"].(bool)
	if count > 1 && !replaceAll {
		return toolError(fmt.Sprintf("old_string occurs %d times; set replace_all to true or provide more context", count))
	}
	updated := strings.Replace(string(data), oldText, newText, 1)
	replacements := 1
	if replaceAll {
		updated = strings.ReplaceAll(string(data), oldText, newText)
		replacements = count
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return toolError(fmt.Sprintf("write file: %v", err))
	}
	return ToolResult{Output: fmt.Sprintf("Updated %s (%d replacement(s))", path, replacements)}
}
