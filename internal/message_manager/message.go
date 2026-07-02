package session

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

// 消息管理
type MessageManager struct {
	history []struct {
		Role           string            // 消息角色
		Content        string            // 消息内容
		ThinkingBlocks []ThinkingBlock   // 思考
		ToolUses       []ToolUseBlock    // 工具
		ToolResults    []ToolResultBlock // 工具调用结果
	}
	// ltmInjected bool // 是否已经注入长期记忆
}
