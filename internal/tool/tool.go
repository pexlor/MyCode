package tool

import "context"

type ToolResult struct {
	Output  string
	IsError bool
}

type Tool interface {
	Name() string
	Description() string
	Schema() map[string]any
	Execute(ctx context.Context, args map[string]any) ToolResult
}
