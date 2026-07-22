package contextmanager

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type recordingSummarizer struct {
	requests []SummarizeRequest
	content  string
	err      error
}

func (s *recordingSummarizer) Summarize(_ context.Context, request SummarizeRequest) (SummarizeResponse, error) {
	s.requests = append(s.requests, request)
	return SummarizeResponse{Content: s.content}, s.err
}

func TestCompactorSummarizesOnlyMessagesAfterCheckpoint(t *testing.T) {
	store, _ := NewFileConversationStore(t.TempDir())
	ctx := context.Background()
	active := SummarySnapshot{Version: 1, SessionID: "s1", CoveredThroughMessageID: "m15", CoveredThroughTurnID: "t15", Content: "summary v1"}
	if err := store.CommitSummary(ctx, active, 0); err != nil {
		t.Fatal(err)
	}
	messages := []StoredMessage{
		{ID: "m16", SessionID: "s1", TurnID: "t16", Content: "new sixteen", TurnStatus: TurnComplete},
		{ID: "m17", SessionID: "s1", TurnID: "t17", Content: "new seventeen", TurnStatus: TurnComplete},
		{ID: "m18", SessionID: "s1", TurnID: "t18", Content: "keep recent", TurnStatus: TurnComplete},
	}
	primary := &recordingSummarizer{content: "summary v2"}
	policy := DefaultPolicy()
	policy.RecentCompleteTurns = 1
	policy.MinCompactionIncrementTokens = 1
	compactor := ConversationCompactor{Store: store, Primary: primary, Estimator: ConservativeEstimator{}, Policy: policy}
	snapshot, changed, err := compactor.Compact(ctx, "s1", active, messages, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || snapshot.Version != 2 || snapshot.CoveredThroughMessageID != "m17" {
		t.Fatalf("snapshot = %#v, changed = %v", snapshot, changed)
	}
	if len(primary.requests) != 1 || len(primary.requests[0].Messages) != 2 {
		t.Fatalf("requests = %#v", primary.requests)
	}
	joined := primary.requests[0].PreviousSummary + primary.requests[0].Messages[0].Content + primary.requests[0].Messages[1].Content
	if strings.Contains(joined, "m15") || !strings.Contains(joined, "new sixteen") {
		t.Fatalf("unexpected incremental input: %q", joined)
	}
}

func TestCompactorDoesNotCallModelWithoutNewEligibleTurn(t *testing.T) {
	primary := &recordingSummarizer{content: "unused"}
	policy := DefaultPolicy()
	policy.RecentCompleteTurns = 1
	policy.MinCompactionIncrementTokens = 1
	compactor := ConversationCompactor{Primary: primary, Estimator: ConservativeEstimator{}, Policy: policy}
	active := SummarySnapshot{Version: 1, SessionID: "s1", CoveredThroughMessageID: "m15", Content: "existing"}
	_, changed, err := compactor.Compact(context.Background(), "s1", active, []StoredMessage{{ID: "m16", TurnID: "t16", TurnStatus: TurnComplete}}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if changed || len(primary.requests) != 0 {
		t.Fatalf("changed = %v, calls = %d", changed, len(primary.requests))
	}
}

func TestCompactorFallsBackOnce(t *testing.T) {
	store, _ := NewFileConversationStore(t.TempDir())
	primary := &recordingSummarizer{err: errors.New("primary failed")}
	fallback := &recordingSummarizer{content: "fallback summary"}
	policy := DefaultPolicy()
	policy.RecentCompleteTurns = 0
	policy.MinCompactionIncrementTokens = 1
	compactor := ConversationCompactor{Store: store, Primary: primary, Fallback: fallback, Estimator: ConservativeEstimator{}, Policy: policy}
	messages := []StoredMessage{{ID: "m1", SessionID: "s1", TurnID: "t1", Content: "content", TurnStatus: TurnComplete}}
	snapshot, changed, err := compactor.Compact(context.Background(), "s1", SummarySnapshot{}, messages, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || snapshot.Content != "fallback summary" || len(primary.requests) != 1 || len(fallback.requests) != 1 {
		t.Fatalf("snapshot = %#v, primary = %d, fallback = %d", snapshot, len(primary.requests), len(fallback.requests))
	}
}
