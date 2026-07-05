package llm

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestOpenAICompatSimpleConversation(t *testing.T) {
	apiKey := os.Getenv("DASHSCOPE_API_KEY")
	if apiKey == "" {
		t.Skip("未设置 DASHSCOPE_API_KEY，跳过集成测试")
	}

	client, err := NewClient(&ModelParm{
		Protocol:  "openai-compat",
		BaseURL:   "https://llm-lgsv9uhdbprfr0vv.cn-beijing.maas.aliyuncs.com/compatible-mode/v1",
		APIKey:    apiKey,
		ModelName: "qwen-plus",
	})
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	eventChan, errChan := client.Stream(&StreamRequest{
		Context:      ctx,
		SystemPrompt: "你是一个友好、简洁的中文助手。",
		Messages: []Message{
			{
				Role:    "user",
				Content: "你好，请简单介绍一下你自己。",
			},
		},
	})

	var response strings.Builder
	for eventChan != nil || errChan != nil {
		select {
		case event, ok := <-eventChan:
			if !ok {
				eventChan = nil
				continue
			}
			if text, ok := event.(TextStream); ok {
				response.WriteString(text.Text)
			}

		case streamErr, ok := <-errChan:
			if !ok {
				errChan = nil
				continue
			}
			if streamErr != nil {
				t.Fatalf("流式请求失败: %v", streamErr)
			}

		case <-ctx.Done():
			t.Fatalf("等待模型响应超时: %v", ctx.Err())
		}
	}

	if response.Len() == 0 {
		t.Fatal("模型返回内容为空")
	}
	t.Logf("模型响应: %s", response.String())
}
