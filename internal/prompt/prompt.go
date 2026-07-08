package prompt

// LLM 是概率模型，指令越模糊，行为空间越大
// system prompt 中包含：
// 静态 system prompt: 角色定义、行为准则、工具使用指南、代码质量规范、安全边界、任务执行模式、输出风格
// 环境上下文：工作目录，当前时间、操作系统
// 后续添加： AGENT.md 、用户偏好、项目知识

func BuildSystemPrompt() string {

	return ""
}
