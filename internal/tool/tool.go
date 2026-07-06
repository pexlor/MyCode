package tool

import "context"

type ToolResult struct {
	Output  string
	IsError bool
}

type ToolSchema struct {
	Name        string
	Description string
	Parameters  map[string]any
}

type Tool interface {
	Name() string
	Description() string
	Schema() *ToolSchema
	Execute(ctx context.Context, args map[string]any) ToolResult
}
