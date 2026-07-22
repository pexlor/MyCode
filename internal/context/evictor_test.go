package contextmanager

import (
	"strings"
	"testing"
)

func TestEvictorReplacesOldResultsBeforeCurrentTurn(t *testing.T) {
	large := strings.Repeat("test output ", 30)
	messages := []StoredMessage{
		{ID: "m1", TurnID: "turn-1", Role: "tool", ToolResults: []StoredToolResult{{ToolUseID: "c1", ToolName: "Bash", Content: large, ArtifactID: "a1", State: ResultFull}}},
		{ID: "m2", TurnID: "turn-2", Role: "tool", ToolResults: []StoredToolResult{{ToolUseID: "c2", ToolName: "Bash", Content: large, ArtifactID: "a2", State: ResultFull}}},
	}
	evictor := StaleResultEvictor{Estimator: ConservativeEstimator{}, Limit: 80}
	got := evictor.Evict(messages, "turn-2")
	if got[0].ToolResults[0].State != ResultReference {
		t.Fatalf("old result state = %q", got[0].ToolResults[0].State)
	}
	if got[0].ToolResults[0].ToolUseID != "c1" || !strings.Contains(got[0].ToolResults[0].Content, "a1") {
		t.Fatalf("old result lost protocol reference: %#v", got[0].ToolResults[0])
	}
	if got[1].ToolResults[0].State != ResultFull {
		t.Fatalf("current turn was evicted: %#v", got[1].ToolResults[0])
	}
}

func TestEvictorDoesNothingWithinBudget(t *testing.T) {
	messages := []StoredMessage{{ID: "m1", TurnID: "turn-1", ToolResults: []StoredToolResult{{ToolUseID: "c1", Content: "ok", State: ResultFull}}}}
	got := (StaleResultEvictor{Estimator: ConservativeEstimator{}, Limit: 100}).Evict(messages, "turn-2")
	if got[0].ToolResults[0].State != ResultFull {
		t.Fatalf("result was evicted within budget: %#v", got)
	}
}
