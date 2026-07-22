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
	// Store 保存完整结果；ContextView 中只保留可恢复引用。
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

// Process 检查候选消息中的工具结果，并按单项阈值和批次阈值决定是否归档。
// 返回的是深拷贝后的上下文视图，不会修改 ConversationStore 中的原始消息。
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
	// 批次超限时优先卸载最大的结果，通常可以用最少的归档次数释放最多 Token。
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
		// 必须先成功保存完整正文，之后才能把 ContextView 中的 Content 替换为引用。
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

// artifactID 由工具调用 ID 和正文共同生成，确保相同结果具有稳定、可重复定位的 ID。
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

// previewText 同时限制行数和字符数，避免单行超长日志绕过预览截断。
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

// cloneStoredMessages 深拷贝会被上下文策略修改的 slice，保证原始候选数据不被污染。
func cloneStoredMessages(messages []StoredMessage) []StoredMessage {
	result := append([]StoredMessage(nil), messages...)
	for index := range result {
		result[index].ToolUses = append([]StoredToolUse(nil), result[index].ToolUses...)
		result[index].ToolResults = append([]StoredToolResult(nil), result[index].ToolResults...)
	}
	return result
}
