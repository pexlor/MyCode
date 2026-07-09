package prompt

import (
	"os"
	"strings"
	"time"
)

const systemPromptPath = "/Users/fengrui03/Desktop/MyCode/internal/prompt/system_prompt.md"

func BuildSystemPrompt() (string, error) {
	staticPrompt, err := buildStaticPrompt()
	if err != nil {
		return "", err
	}
	environmentPrompt, err := buildEnvironmentPrompt()
	if err != nil {
		return "", err
	}
	// todo: 后续添加 Agent.md
	sections := []string{
		staticPrompt,
		environmentPrompt,
	}
	return strings.Join(compactSections(sections), "\n\n"), nil
}

func buildEnvironmentPrompt() (string, error) {
	var builder strings.Builder

	now := time.Now()                   // 获取当前时间
	workingDirectory, err := os.Getwd() // 获取当前进程的工作目录
	if err != nil {
		return "", err
	}

	builder.WriteString("# Environment\n\n")
	builder.WriteString("- Current working directory: ")
	builder.WriteString(workingDirectory)
	builder.WriteString("\n")
	builder.WriteString("- Current time: ")
	builder.WriteString(now.Format("2006-01-02 15:04:05 MST"))
	return builder.String(), nil
}

func buildStaticPrompt() (string, error) {
	staticPrompt, err := os.ReadFile(systemPromptPath)
	return string(staticPrompt), err
}

func compactSections(sections []string) []string {
	result := make([]string, 0, len(sections))
	for _, section := range sections {
		section = strings.TrimSpace(section)
		if section != "" {
			result = append(result, section)
		}
	}
	return result
}
