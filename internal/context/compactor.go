package contextmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type SummarizeRequest struct {
	PreviousSummary                 string
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

type ConversationCompactor struct {
	Store     ConversationStore
	Primary   Summarizer
	Fallback  Summarizer
	Estimator TokenEstimator
	Model     string
	Policy    ContextPolicy
}

func (c ConversationCompactor) Compact(
	ctx context.Context,
	sessionID string,
	active SummarySnapshot,
	messages []StoredMessage,
	tokenBudget int,
) (SummarySnapshot, bool, error) {
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
	content, err := c.callSummarizers(ctx, request)
	if err != nil {
		content = deterministicSummary(active.Content, eligible)
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
	if err := c.Store.CommitSummary(ctx, snapshot, active.Version); err != nil {
		return active, false, err
	}
	return snapshot, true, nil
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
