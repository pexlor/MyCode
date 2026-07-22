package contextmanager

import (
	"encoding/json"
	"math"

	"MyCode/internal/message"
	"MyCode/internal/tool"
)

type TokenEstimator interface {
	EstimateText(model, text string) int
	EstimateMessages(model string, messages []message.Message) int
	EstimateTools(model string, tools []*tool.ToolSchema) int
}

// ConservativeEstimator 是缺少目标模型 tokenizer 时的保守降级实现。
// 它宁可略微高估，也不让请求在模型侧才发现超过上下文窗口。
type ConservativeEstimator struct{}

// EstimateText 使用 UTF-8 字节数估算 Token，并额外增加 15% 安全系数。
// 该算法不是精确 tokenizer，后续可以通过 TokenEstimator 接口替换。
func (ConservativeEstimator) EstimateText(_ string, text string) int {
	if text == "" {
		return 0
	}
	return int(math.Ceil(float64(len([]byte(text))) / 3 * 1.15))
}

func (e ConservativeEstimator) EstimateMessages(model string, messages []message.Message) int {
	total := 0
	for _, item := range messages {
		data, _ := json.Marshal(item)
		// 每条消息额外计入角色和协议包装的固定开销。
		total += e.EstimateText(model, string(data)) + 4
	}
	return total
}

func (e ConservativeEstimator) EstimateTools(model string, schemas []*tool.ToolSchema) int {
	if len(schemas) == 0 {
		return 0
	}
	data, _ := json.Marshal(schemas)
	return e.EstimateText(model, string(data)) + 4
}
