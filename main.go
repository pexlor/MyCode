package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"MyCode/internal/llm"
	"MyCode/internal/tool"
)

const maxToolRounds = 8

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	apiKey := os.Getenv("DASHSCOPE_API_KEY")
	if apiKey == "" {
		return errors.New("未设置环境变量 DASHSCOPE_API_KEY")
	}

	client, err := llm.NewClient(&llm.ModelParm{
		Protocol:  "openai-compat",
		BaseURL:   "https://llm-lgsv9uhdbprfr0vv.cn-beijing.maas.aliyuncs.com/compatible-mode/v1",
		APIKey:    apiKey,
		ModelName: "qwen-plus",
	})
	if err != nil {
		return fmt.Errorf("创建客户端失败: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	toolManager := tool.CreateDefaultTools()
	schemas, err := buildToolSchemas(toolManager)
	if err != nil {
		return err
	}

	messages := []llm.Message{
		{Role: "user", Content: "你好，请简单介绍一下你自己。然后读取一下 doc/product design.md "},
	}
	response, err := runConversation(
		ctx,
		client,
		toolManager,
		"你是一个友好、简洁的中文助手。",
		messages,
		schemas,
	)
	if err != nil {
		return err
	}

	fmt.Printf("模型响应: %s\n", response)
	return nil
}

func buildToolSchemas(manager *tool.ToolsManager) ([]llm.ToolSchema, error) {
	rawSchemas := manager.BuildAllSchemas()
	schemas := make([]llm.ToolSchema, 0, len(rawSchemas))
	for i, schema := range rawSchemas {
		name, ok := schema["name"].(string)
		if !ok || name == "" {
			return nil, fmt.Errorf("第 %d 个工具的 name 无效", i+1)
		}
		description, ok := schema["description"].(string)
		if !ok {
			return nil, fmt.Errorf("工具 %q 的 description 无效", name)
		}
		parameters, ok := schema["input_schema"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("工具 %q 的 input_schema 无效", name)
		}

		schemas = append(schemas, llm.ToolSchema{
			Name:        name,
			Description: description,
			Parameters:  parameters,
		})
	}
	return schemas, nil
}

func runConversation(
	ctx context.Context,
	client llm.LLMClient,
	toolManager *tool.ToolsManager,
	systemPrompt string,
	messages []llm.Message,
	schemas []llm.ToolSchema,
) (string, error) {
	var response strings.Builder

	for round := 0; round < maxToolRounds; round++ {
		eventChan, errChan := client.Stream(&llm.StreamRequest{
			Context:      ctx,
			SystemPrompt: systemPrompt,
			Messages:     messages,
			Tools:        schemas,
		})

		var assistantText strings.Builder
		var toolCalls []llm.ToolCallComplete
		for eventChan != nil || errChan != nil {
			select {
			case event, ok := <-eventChan:
				if !ok {
					eventChan = nil
					continue
				}
				switch event := event.(type) {
				case llm.TextStream:
					assistantText.WriteString(event.Text)
					response.WriteString(event.Text)
				case llm.ToolCallStart:
					log.Printf("模型请求调用工具 %s", event.ToolName)
				case llm.ToolCallComplete:
					toolCalls = append(toolCalls, event)
				}

			case streamErr, ok := <-errChan:
				if !ok {
					errChan = nil
					continue
				}
				if streamErr != nil {
					return "", fmt.Errorf("流式请求失败: %w", streamErr)
				}

			case <-ctx.Done():
				return "", fmt.Errorf("等待模型响应超时: %w", ctx.Err())
			}
		}

		if len(toolCalls) == 0 {
			if response.Len() == 0 {
				return "", errors.New("模型返回内容为空")
			}
			return response.String(), nil
		}

		assistantCalls := make([]llm.ToolCall, 0, len(toolCalls))
		for _, call := range toolCalls {
			if call.ToolID == "" || call.ToolName == "" {
				return "", errors.New("模型返回了无效的工具调用")
			}
			argumentsJSON, err := json.Marshal(call.Arguments)
			if err != nil {
				return "", fmt.Errorf("编码工具 %q 的参数失败: %w", call.ToolName, err)
			}
			assistantCalls = append(assistantCalls, llm.ToolCall{
				ID:        call.ToolID,
				Name:      call.ToolName,
				Arguments: string(argumentsJSON),
			})
		}
		messages = append(messages, llm.Message{
			Role:      "assistant",
			Content:   assistantText.String(),
			ToolCalls: assistantCalls,
		})

		for _, call := range toolCalls {
			result := executeTool(ctx, toolManager, call)
			messages = append(messages, llm.Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: call.ToolID,
			})
		}
	}

	return "", fmt.Errorf("工具调用超过最大轮数 %d", maxToolRounds)
}

func executeTool(ctx context.Context, manager *tool.ToolsManager, call llm.ToolCallComplete) string {
	registeredTool := manager.GetTool(call.ToolName)
	if registeredTool == nil {
		return fmt.Sprintf("工具执行失败: 未注册工具 %q", call.ToolName)
	}

	result := registeredTool.Execute(ctx, call.Arguments)
	if result.IsError {
		log.Printf("工具 %s 执行失败", call.ToolName)
		return "工具执行失败: " + result.Output
	}
	log.Printf("工具 %s 执行完成", call.ToolName)
	return result.Output
}
