package contextmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type SummarizeRequest struct {
	// PreviousSummary 是上一版已经生效的任务状态，而不是全部原始历史。
	PreviousSummary string
	// PreviousCoveredThroughMessageID 明确增量起点，便于测试和审计重复压缩问题。
	PreviousCoveredThroughMessageID string
	Messages                        []StoredMessage
	ArtifactIndex                   []ToolArtifact
	TokenBudget                     int
}

type SummarizeResponse struct {
	Content string
}

type Summarizer interface {
	Summarize(context.Context, SummarizeRequest) (SummarizeResponse, error)
}

// ConversationCompactor 负责第 3 层增量压缩。
// 它只接收 active checkpoint 之后的消息，并通过 CommitSummary 原子推进覆盖游标。
type ConversationCompactor struct {
	Store     ConversationStore
	Primary   Summarizer
	Fallback  Summarizer
	Estimator TokenEstimator
	Model     string
	Policy    ContextPolicy
}

// Compact 尝试把较早的完整 Turn 合并进新摘要。
// changed=false 表示没有足够的新内容，本次不会调用摘要模型，也不会推进检查点。
func (c ConversationCompactor) Compact(
	ctx context.Context,
	sessionID string,
	active SummarySnapshot,
	messages []StoredMessage,
	tokenBudget int,
) (SummarySnapshot, bool, error) {
	// 保留最近若干完整 Turn，避免刚发生的细节过早变成有损摘要。
	eligible := compactableMessages(messages, c.Policy.RecentCompleteTurns)
	if len(eligible) == 0 {
		return active, false, nil
	}
	if c.Estimator == nil {
		return active, false, errors.New("compactor estimator is required")
	}
	increment := estimateStoredMessages(c.Estimator, c.Model, eligible)
	if increment < c.Policy.MinCompactionIncrementTokens {
		return active, false, nil
	}
	if c.Store == nil {
		return active, false, errors.New("compactor store is required")
	}
	request := SummarizeRequest{
		PreviousSummary:                 active.Content,
		PreviousCoveredThroughMessageID: active.CoveredThroughMessageID,
		Messages:                        eligible,
		TokenBudget:                     tokenBudget,
	}
	// 独立摘要模型失败时只回退当前模型一次；两者都失败则使用确定性最小摘要。
	content, err := c.callSummarizers(ctx, request)
	if err != nil {
		content = deterministicSummary(active.Content, eligible)
		content = fitTextToTokenBudget(c.Estimator, c.Model, content, tokenBudget)
	}
	if strings.TrimSpace(content) == "" {
		return active, false, errors.New("compactor produced an empty summary")
	}
	if tokenBudget > 0 && c.Estimator.EstimateText(c.Model, content) > tokenBudget {
		return active, false, errors.New("compactor summary exceeds token budget")
	}
	last := eligible[len(eligible)-1]
	snapshot := SummarySnapshot{
		Version:                 active.Version + 1,
		SessionID:               sessionID,
		CoveredThroughMessageID: last.ID,
		CoveredThroughTurnID:    last.TurnID,
		PreviousSummaryVersion:  active.Version,
		Content:                 content,
		TokenEstimate:           c.Estimator.EstimateText(c.Model, content),
	}
	// CommitSummary 成功前旧摘要始终有效，所以上层不会看到“游标已推进但摘要丢失”。
	if err := c.Store.CommitSummary(ctx, snapshot, active.Version); err != nil {
		return active, false, err
	}
	return snapshot, true, nil
}

// fitTextToTokenBudget 用 Rune 边界截断确定性摘要，避免切坏 UTF-8 中文字符。
func fitTextToTokenBudget(estimator TokenEstimator, model, content string, budget int) string {
	if budget <= 0 || estimator.EstimateText(model, content) <= budget {
		return content
	}
	runes := []rune(content)
	low, high := 0, len(runes)
	for low < high {
		middle := (low + high + 1) / 2
		candidate := string(runes[:middle]) + "\n[deterministic summary truncated]"
		if estimator.EstimateText(model, candidate) <= budget {
			low = middle
		} else {
			high = middle - 1
		}
	}
	result := string(runes[:low]) + "\n[deterministic summary truncated]"
	if estimator.EstimateText(model, result) <= budget {
		return result
	}
	return "summary truncated"
}

func (c ConversationCompactor) callSummarizers(ctx context.Context, request SummarizeRequest) (string, error) {
	if c.Primary != nil {
		response, err := c.Primary.Summarize(ctx, request)
		if err == nil && strings.TrimSpace(response.Content) != "" {
			return response.Content, nil
		}
	}
	if c.Fallback != nil {
		response, err := c.Fallback.Summarize(ctx, request)
		if err == nil && strings.TrimSpace(response.Content) != "" {
			return response.Content, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", errors.New("all summarizers failed")
}

// compactableMessages 以 Turn 为单位选择可压缩范围，绝不拆开工具调用与结果。
func compactableMessages(messages []StoredMessage, recentTurns int) []StoredMessage {
	var completedTurns []string
	seen := make(map[string]bool)
	for _, message := range messages {
		if message.TurnStatus != TurnComplete || message.TurnID == "" || seen[message.TurnID] {
			continue
		}
		seen[message.TurnID] = true
		completedTurns = append(completedTurns, message.TurnID)
	}
	compressCount := len(completedTurns) - max(recentTurns, 0)
	if compressCount <= 0 {
		return nil
	}
	compressible := make(map[string]bool, compressCount)
	for _, turnID := range completedTurns[:compressCount] {
		compressible[turnID] = true
	}
	var result []StoredMessage
	for _, message := range messages {
		if compressible[message.TurnID] {
			result = append(result, message)
		}
	}
	return result
}

func estimateStoredMessages(estimator TokenEstimator, model string, messages []StoredMessage) int {
	total := 0
	for _, message := range messages {
		total += estimator.EstimateText(model, message.Content) + 4
		for _, result := range message.ToolResults {
			total += estimator.EstimateText(model, result.Content) + 4
		}
	}
	return total
}

// deterministicSummary 是摘要模型全部不可用时的降级路径。
// 它只复述已有消息和工具结论，不产生新的推断或决策。
func deterministicSummary(previous string, messages []StoredMessage) string {
	var builder strings.Builder
	builder.WriteString("## Previous task state\n")
	if strings.TrimSpace(previous) == "" {
		builder.WriteString("No previous summary.\n")
	} else {
		builder.WriteString(previous)
		builder.WriteByte('\n')
	}
	builder.WriteString("## Newly compacted turns\n")
	for _, message := range messages {
		fmt.Fprintf(&builder, "- [%s/%s] %s\n", message.TurnID, message.Role, previewText(message.Content, 4))
		for _, result := range message.ToolResults {
			fmt.Fprintf(&builder, "  - tool %s (%s): %s\n", result.ToolName, result.ToolUseID, previewText(result.Content, 2))
		}
	}
	return builder.String()
}
