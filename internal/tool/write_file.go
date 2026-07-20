package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

const WriteFileDescription = `Create or replace a text file.
Use this tool when the complete desired file contents are known. Parent directories are created automatically.`

type WriteFileTool struct{}

func (t *WriteFileTool) Name() string        { return "WriteFile" }
func (t *WriteFileTool) Description() string { return WriteFileDescription }
func (t *WriteFileTool) Schema() *ToolSchema {
	return &ToolSchema{Name: t.Name(), Description: t.Description(), Parameters: map[string]any{
		"type": "object", "properties": map[string]any{
			"file_path": map[string]any{"type": "string", "description": "File to create or replace."},
			"content":   map[string]any{"type": "string", "description": "Complete UTF-8 file contents."},
		}, "required": []string{"file_path", "content"},
	}}
}

func (t *WriteFileTool) Execute(ctx context.Context, args map[string]any) ToolResult {
	path, _ := args["file_path"].(string)
	if path == "" {
		return toolError("file_path is required and must be a string")
	}
	content, ok := args["content"].(string)
	if !ok {
		return toolError("content is required and must be a string")
	}
	if err := ctx.Err(); err != nil {
		return toolError(err.Error())
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return toolError(fmt.Sprintf("create parent directories: %v", err))
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return toolError(fmt.Sprintf("write file: %v", err))
	}
	return ToolResult{Output: fmt.Sprintf("Wrote %s", path)}
}
