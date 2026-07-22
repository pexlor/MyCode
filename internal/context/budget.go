package contextmanager

import (
	"errors"
	"math"
)

type ModelContextSpec struct {
	// ModelName 用于选择 Token 估算规则；当前 fallback 估算器不会区分模型。
	ModelName string
	// ContextWindow 是输入与输出合计的模型窗口大小。
	ContextWindow int
	// MaxOutputTokens 为本次回复预留的最大输出空间。
	MaxOutputTokens int
}

// ContextPolicy 保存可调节的上下文治理策略，不包含某个模型的绝对 Token 数。
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

// DefaultPolicy 返回偏保守的 MVP 默认值。
// 75% 是相对于 HardInputLimit 的软压缩线，而不是相对于模型完整窗口。
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

// ContextBudget 是把模型窗口、输出预留和百分比策略换算后的绝对 Token 预算。
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

// NewBudget 先扣除输出、下一轮工具结果和安全余量，再计算历史上下文预算。
// 这样即使当前输入尚未占满窗口，也不会因为模型输出或新工具结果突然溢出。
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
	// 未显式设置输出上限时，最多预留 8192 Token；小窗口按 10% 计算。
	output := model.MaxOutputTokens
	if output <= 0 {
		output = min(policy.DefaultOutputTokens, int(math.Ceil(float64(model.ContextWindow)*0.10)))
	}
	toolReserve := int(float64(model.ContextWindow) * policy.ReservedToolResultRatio)
	safety := int(float64(model.ContextWindow) * policy.SafetyMarginRatio)
	// HardInputLimit 才是 System Prompt、工具 schema 和历史消息真正可使用的上限。
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
