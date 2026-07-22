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

type ConservativeEstimator struct{}

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
