package contextmanager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

type ResultOffloader struct {
	Store       ConversationStore
	Estimator   TokenEstimator
	Model       string
	SingleLimit int
	BatchLimit  int
}

type resultLocation struct {
	messageIndex int
	resultIndex  int
	tokens       int
}

func (o ResultOffloader) Process(ctx context.Context, sessionID string, messages []StoredMessage) ([]StoredMessage, error) {
	result := cloneStoredMessages(messages)
	if o.Store == nil || o.Estimator == nil {
		return nil, fmt.Errorf("offloader store and estimator are required")
	}
	var locations []resultLocation
	total := 0
	for messageIndex := range result {
		for resultIndex := range result[messageIndex].ToolResults {
			toolResult := result[messageIndex].ToolResults[resultIndex]
			if toolResult.State == ResultReference || toolResult.State == ResultDropped {
				continue
			}
			tokens := o.Estimator.EstimateText(o.Model, toolResult.Content)
			total += tokens
			locations = append(locations, resultLocation{messageIndex: messageIndex, resultIndex: resultIndex, tokens: tokens})
		}
	}
	sort.SliceStable(locations, func(i, j int) bool { return locations[i].tokens > locations[j].tokens })
	for _, location := range locations {
		mustArchive := o.SingleLimit > 0 && location.tokens > o.SingleLimit
		if !mustArchive && (o.BatchLimit <= 0 || total <= o.BatchLimit) {
			continue
		}
		toolResult := &result[location.messageIndex].ToolResults[location.resultIndex]
		artifactID := artifactID(toolResult.ToolUseID, toolResult.Content)
		artifact := ToolArtifact{
			ID:            artifactID,
			SessionID:     sessionID,
			ToolUseID:     toolResult.ToolUseID,
			ToolName:      toolResult.ToolName,
			CreatedAt:     time.Now(),
			IsError:       toolResult.IsError,
			TokenEstimate: location.tokens,
			Preview:       previewText(toolResult.Content, 12),
		}
		if err := o.Store.SaveToolArtifact(ctx, artifact, strings.NewReader(toolResult.Content)); err != nil {
			return nil, err
		}
		toolResult.ArtifactID = artifactID
		toolResult.State = ResultReference
		toolResult.Content = renderArtifactReference(artifact, toolResult.Content)
		total -= location.tokens
	}
	return result, nil
}

func artifactID(toolUseID, content string) string {
	digest := sha256.Sum256([]byte(toolUseID + "\x00" + content))
	return "artifact-" + hex.EncodeToString(digest[:8])
}

func renderArtifactReference(artifact ToolArtifact, content string) string {
	status := "success"
	if artifact.IsError {
		status = "error"
	}
	return fmt.Sprintf("[tool result archived]\ntool: %s\nstatus: %s\nartifact_id: %s\ntokens: %d\npreview:\n%s\nRead the artifact for exact content.",
		artifact.ToolName, status, artifact.ID, artifact.TokenEstimate, previewText(content, 12))
}

func previewText(content string, maxLines int) string {
	const maxCharacters = 240
	if len(content) > maxCharacters {
		const half = maxCharacters / 2
		content = content[:half] + "\n... truncated ...\n" + content[len(content)-half:]
	}
	lines := strings.Split(content, "\n")
	if len(lines) <= maxLines {
		return content
	}
	half := maxLines / 2
	return strings.Join(lines[:half], "\n") + "\n... truncated ...\n" + strings.Join(lines[len(lines)-half:], "\n")
}

func cloneStoredMessages(messages []StoredMessage) []StoredMessage {
	result := append([]StoredMessage(nil), messages...)
	for index := range result {
		result[index].ToolUses = append([]StoredToolUse(nil), result[index].ToolUses...)
		result[index].ToolResults = append([]StoredToolResult(nil), result[index].ToolResults...)
	}
	return result
}
