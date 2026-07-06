package message

// 工具调用
type ToolUseBlock struct {
	ToolUseID string
	ToolName  string
	Arguments map[string]any // 工具参数
}

// 工具调用结果
type ToolResultBlock struct {
	ToolUseID string
	Content   string // 包括 error信息和正常调用结果
	IsError   bool
}

// 思考内容
type ThinkingBlock struct {
	Thinking string
	// Signature string
}

type Message struct {
	Role           string            // 消息角色
	Content        string            // 普通消息内容
	ThinkingBlocks []ThinkingBlock   // 思考消息内容
	ToolUses       []ToolUseBlock    // 工具调用内容
	ToolResults    []ToolResultBlock // 工具调用结果
}

// 消息管理
type MessageManager struct {
	SystemPrompt string
	History      []Message // 顺序写入
}
