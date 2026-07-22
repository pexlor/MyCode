package contextmanager

import "fmt"

type StaleResultEvictor struct {
	Estimator TokenEstimator
	Model     string
	Limit     int
}

// Evict 在工具历史超过独立预算时，按时间从旧到新降级结果。
// 当前 Turn 永远不被本层淘汰；即使正文被移除，也保留 ToolUseID 以维持协议配对。
func (e StaleResultEvictor) Evict(messages []StoredMessage, currentTurnID string) []StoredMessage {
	result := cloneStoredMessages(messages)
	if e.Estimator == nil || e.Limit <= 0 {
		return result
	}
	total := toolResultTokens(e.Estimator, e.Model, result)
	if total <= e.Limit {
		return result
	}
	// StoredMessage 保持 transcript 顺序，因此正向遍历天然优先处理最旧结果。
	for messageIndex := range result {
		if result[messageIndex].TurnID == currentTurnID {
			continue
		}
		for resultIndex := range result[messageIndex].ToolResults {
			item := &result[messageIndex].ToolResults[resultIndex]
			if item.State != ResultFull && item.State != "" {
				continue
			}
			oldTokens := e.Estimator.EstimateText(e.Model, item.Content)
			item.State = ResultReference
			if item.ArtifactID != "" {
				// 已有 Artifact 时留下可恢复引用；否则明确提示只能从 transcript 恢复。
				item.Content = fmt.Sprintf("[stale tool result evicted]\nartifact_id: %s\nRead the artifact for exact content.", item.ArtifactID)
			} else {
				item.Content = "[stale tool result evicted; original remains in session transcript]"
			}
			total -= oldTokens - e.Estimator.EstimateText(e.Model, item.Content)
			if total <= e.Limit {
				return result
			}
		}
	}
	return result
}

func toolResultTokens(estimator TokenEstimator, model string, messages []StoredMessage) int {
	total := 0
	for _, stored := range messages {
		for _, result := range stored.ToolResults {
			total += estimator.EstimateText(model, result.Content)
		}
	}
	return total
}
