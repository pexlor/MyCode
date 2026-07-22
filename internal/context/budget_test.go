package contextmanager

import (
	"testing"

	"MyCode/internal/message"
	"MyCode/internal/tool"
)

func TestNewBudgetReservesOutputToolsAndMargin(t *testing.T) {
	policy := DefaultPolicy()
	budget, err := NewBudget(ModelContextSpec{ContextWindow: 100_000, MaxOutputTokens: 10_000}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if budget.HardInputLimit != 75_000 {
		t.Fatalf("hard input limit = %d, want 75000", budget.HardInputLimit)
	}
	if budget.SoftCompactLimit != 56_250 {
		t.Fatalf("soft compact limit = %d, want 56250", budget.SoftCompactLimit)
	}
	if budget.ToolHistoryLimit != 18_750 {
		t.Fatalf("tool history limit = %d, want 18750", budget.ToolHistoryLimit)
	}
}

func TestNewBudgetRejectsInvalidPolicy(t *testing.T) {
	policy := DefaultPolicy()
	policy.SoftCompactRatio = 1.1
	if _, err := NewBudget(ModelContextSpec{ContextWindow: 100_000}, policy); err == nil {
		t.Fatal("expected invalid policy error")
	}
	if _, err := NewBudget(ModelContextSpec{ContextWindow: 0}, DefaultPolicy()); err == nil {
		t.Fatal("expected invalid context window error")
	}
}

func TestConservativeEstimatorCountsMessagesAndTools(t *testing.T) {
	estimator := ConservativeEstimator{}
	plain := estimator.EstimateText("model", "你好，context")
	if plain <= 0 {
		t.Fatalf("plain estimate = %d", plain)
	}
	messages := estimator.EstimateMessages("model", []message.Message{{Role: message.USER, Content: "你好，context"}})
	if messages <= plain {
		t.Fatalf("message estimate = %d, plain = %d", messages, plain)
	}
	tools := estimator.EstimateTools("model", []*tool.ToolSchema{{Name: "ReadFile", Description: "read a file"}})
	if tools <= 0 {
		t.Fatalf("tool estimate = %d", tools)
	}
}
