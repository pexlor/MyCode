package contextmanager

import "fmt"

type StaleResultEvictor struct {
	Estimator TokenEstimator
	Model     string
	Limit     int
}

func (e StaleResultEvictor) Evict(messages []StoredMessage, currentTurnID string) []StoredMessage {
	result := cloneStoredMessages(messages)
	if e.Estimator == nil || e.Limit <= 0 {
		return result
	}
	total := toolResultTokens(e.Estimator, e.Model, result)
	if total <= e.Limit {
		return result
	}
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
