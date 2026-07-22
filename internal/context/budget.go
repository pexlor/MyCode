package contextmanager

import (
	"errors"
	"math"
)

type ModelContextSpec struct {
	ModelName       string
	ContextWindow   int
	MaxOutputTokens int
}

type ContextPolicy struct {
	SoftCompactRatio             float64
	SafetyMarginRatio            float64
	ReservedToolResultRatio      float64
	ToolHistoryRatio             float64
	SingleToolResultRatio        float64
	ToolBatchRatio               float64
	DefaultOutputTokens          int
	RecentCompleteTurns          int
	MinCompactionIncrementTokens int
}

func DefaultPolicy() ContextPolicy {
	return ContextPolicy{
		SoftCompactRatio:             0.75,
		SafetyMarginRatio:            0.05,
		ReservedToolResultRatio:      0.10,
		ToolHistoryRatio:             0.25,
		SingleToolResultRatio:        0.05,
		ToolBatchRatio:               0.15,
		DefaultOutputTokens:          8192,
		RecentCompleteTurns:          3,
		MinCompactionIncrementTokens: 4000,
	}
}

type ContextBudget struct {
	ContextWindow         int
	ReservedOutput        int
	ReservedToolResults   int
	SafetyMargin          int
	HardInputLimit        int
	SoftCompactLimit      int
	ToolHistoryLimit      int
	SingleToolResultLimit int
	ToolBatchLimit        int
}

func NewBudget(model ModelContextSpec, policy ContextPolicy) (ContextBudget, error) {
	if model.ContextWindow <= 0 {
		return ContextBudget{}, errors.New("context window must be positive")
	}
	for _, ratio := range []float64{
		policy.SoftCompactRatio,
		policy.SafetyMarginRatio,
		policy.ReservedToolResultRatio,
		policy.ToolHistoryRatio,
		policy.SingleToolResultRatio,
		policy.ToolBatchRatio,
	} {
		if ratio < 0 || ratio >= 1 {
			return ContextBudget{}, errors.New("context ratios must be between zero and one")
		}
	}
	if policy.SoftCompactRatio == 0 || policy.ToolHistoryRatio == 0 {
		return ContextBudget{}, errors.New("compact and tool history ratios must be positive")
	}
	output := model.MaxOutputTokens
	if output <= 0 {
		output = min(policy.DefaultOutputTokens, int(math.Ceil(float64(model.ContextWindow)*0.10)))
	}
	toolReserve := int(float64(model.ContextWindow) * policy.ReservedToolResultRatio)
	safety := int(float64(model.ContextWindow) * policy.SafetyMarginRatio)
	hard := model.ContextWindow - output - toolReserve - safety
	if hard <= 0 {
		return ContextBudget{}, errors.New("context reserves consume the entire window")
	}
	return ContextBudget{
		ContextWindow:         model.ContextWindow,
		ReservedOutput:        output,
		ReservedToolResults:   toolReserve,
		SafetyMargin:          safety,
		HardInputLimit:        hard,
		SoftCompactLimit:      int(float64(hard) * policy.SoftCompactRatio),
		ToolHistoryLimit:      int(float64(hard) * policy.ToolHistoryRatio),
		SingleToolResultLimit: min(2000, int(float64(hard)*policy.SingleToolResultRatio)),
		ToolBatchLimit:        min(6000, int(float64(hard)*policy.ToolBatchRatio)),
	}, nil
}
