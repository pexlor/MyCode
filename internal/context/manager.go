package contextmanager

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"MyCode/internal/message"
	"MyCode/internal/tool"
)

var ErrContextBudgetExceeded = errors.New("context budget exceeded")

// LayerReport 记录某一级策略执行前后的 Token 变化，便于未来实现 /context 诊断命令。
type LayerReport struct {
	Layer        string
	BeforeTokens int
	AfterTokens  int
	AffectedIDs  []string
	Reason       string
}

// ContextView 是一次请求真正发送给模型的临时视图。
// 它可以包含摘要和 Artifact 引用，但不会反向覆盖 ConversationStore 中的原始事实。
type ContextView struct {
	SystemPrompt    string
	Messages        []message.Message
	Tools           []*tool.ToolSchema
	EstimatedTokens int
	Budget          ContextBudget
	AppliedLayers   []LayerReport
}

// BuildInput 汇总构建模型请求所需的运行时信息。
type BuildInput struct {
	SessionID      string
	CurrentTurnID  string
	SystemPrompt   string
	CurrentRequest string
	History        []message.Message
	AvailableTools []*tool.ToolSchema
}

// ContextManagerConfig 注入存储、预算、模型和摘要器依赖，便于单元测试替换。
type ContextManagerConfig struct {
	Store     ConversationStore
	Estimator TokenEstimator
	Policy    ContextPolicy
	Model     ModelContextSpec
	Workspace string
	Primary   Summarizer
	Fallback  Summarizer
}

// ContextManager 编排四级触发式上下文管理。
// 四级组件并非每轮全部执行：只有 DemandLoader 每次装配，其余组件都受阈值控制。
type ContextManager struct {
	store     ConversationStore
	estimator TokenEstimator
	policy    ContextPolicy
	model     ModelContextSpec
	budget    ContextBudget
	loader    DemandLoader
	offloader ResultOffloader
	evictor   StaleResultEvictor
	compactor ConversationCompactor

	// syncedCount 和 turnCount 记录当前进程已经写入 Store 的 History 位置，
	// 避免同一条内存消息在多次 Agent iteration 中重复追加到 transcript。
	mu          sync.Mutex
	syncedCount map[string]int
	turnCount   map[string]int
}

// NewContextManager 校验模型预算并组装四级组件。
func NewContextManager(config ContextManagerConfig) (*ContextManager, error) {
	if config.Store == nil || config.Estimator == nil {
		return nil, errors.New("context store and estimator are required")
	}
	budget, err := NewBudget(config.Model, config.Policy)
	if err != nil {
		return nil, err
	}
	manager := &ContextManager{
		store:       config.Store,
		estimator:   config.Estimator,
		policy:      config.Policy,
		model:       config.Model,
		budget:      budget,
		loader:      DemandLoader{Workspace: config.Workspace},
		syncedCount: make(map[string]int),
		turnCount:   make(map[string]int),
	}
	manager.offloader = ResultOffloader{
		Store: config.Store, Estimator: config.Estimator, Model: config.Model.ModelName,
		SingleLimit: budget.SingleToolResultLimit, BatchLimit: budget.ToolBatchLimit,
	}
	manager.evictor = StaleResultEvictor{Estimator: config.Estimator, Model: config.Model.ModelName, Limit: budget.ToolHistoryLimit}
	manager.compactor = ConversationCompactor{
		Store: config.Store, Primary: config.Primary, Fallback: config.Fallback,
		Estimator: config.Estimator, Model: config.Model.ModelName, Policy: config.Policy,
	}
	return manager, nil
}

// Build 生成下一次模型调用使用的 ContextView。
// 最关键的约束是：存在 active summary 时，只读取 CoveredThroughMessageID 之后的原文，
// 因此已经压缩过的消息不会在下一轮再次进入模型，也不会被重复摘要。
func (m *ContextManager) Build(ctx context.Context, input BuildInput) (*ContextView, error) {
	if !validIdentifier(input.SessionID) {
		return nil, ErrInvalidIdentifier
	}
	if input.History != nil {
		if err := m.syncHistory(ctx, input.SessionID, input.History); err != nil {
			return nil, err
		}
	}
	// active summary 与覆盖游标共同构成持久化压缩检查点。
	active, err := m.store.ActiveSummary(ctx, input.SessionID)
	if err != nil {
		return nil, err
	}
	stored, err := m.messagesForView(ctx, input.SessionID, active)
	if err != nil {
		return nil, err
	}
	// 第 1 层：新出现的大工具结果超过单项或批次阈值时才归档。
	beforeTools := toolResultTokens(m.estimator, m.model.ModelName, stored)
	stored, err = m.offloader.Process(ctx, input.SessionID, stored)
	if err != nil {
		return nil, err
	}
	reports := []LayerReport{}
	afterOffload := toolResultTokens(m.estimator, m.model.ModelName, stored)
	if afterOffload != beforeTools {
		reports = append(reports, LayerReport{Layer: "result_offloader", BeforeTokens: beforeTools, AfterTokens: afterOffload, Reason: "tool result threshold exceeded"})
	}
	// 第 2 层：只有工具历史整体超过独立预算时才淘汰旧结果。
	if afterOffload > m.budget.ToolHistoryLimit {
		before := afterOffload
		stored = m.evictor.Evict(stored, input.CurrentTurnID)
		after := toolResultTokens(m.estimator, m.model.ModelName, stored)
		reports = append(reports, LayerReport{Layer: "stale_result_evictor", BeforeTokens: before, AfterTokens: after, Reason: "tool history budget exceeded"})
	}

	// 第 0 层：每次根据活跃路径和当前请求重新装配规则及工具定义。
	rules, err := m.loader.LoadRules(activePaths(stored))
	if err != nil {
		return nil, err
	}
	systemPrompt := appendRules(input.SystemPrompt, rules)
	selectedTools := m.loader.SelectTools(input.CurrentRequest, activeToolNames(stored), input.AvailableTools)
	view := m.renderView(systemPrompt, active, stored, selectedTools, reports)
	// 第 3 层：前三层处理后仍达到软阈值，才尝试增量摘要。
	// Compactor 内部还会检查是否存在足够的新完整 Turn，避免无意义地重复调用模型。
	if view.EstimatedTokens >= m.budget.SoftCompactLimit {
		current := SummarySnapshot{SessionID: input.SessionID}
		if active != nil {
			current = *active
		}
		updated, changed, compactErr := m.compactor.Compact(ctx, input.SessionID, current, stored, max(1, m.budget.HardInputLimit/4))
		if compactErr != nil {
			return nil, compactErr
		}
		if changed {
			// 摘要提交成功后立即从新游标之后重新读取，确保当前请求不再携带已覆盖原文。
			stored, err = m.store.ListMessagesAfter(ctx, input.SessionID, updated.CoveredThroughMessageID)
			if err != nil {
				return nil, err
			}
			stored, err = m.offloader.Process(ctx, input.SessionID, stored)
			if err != nil {
				return nil, err
			}
			reports = append(reports, LayerReport{Layer: "conversation_compactor", BeforeTokens: view.EstimatedTokens, Reason: "soft compact limit reached"})
			active = &updated
			view = m.renderView(systemPrompt, active, stored, selectedTools, reports)
		}
	}
	// BudgetGuard 是发送请求前的最后防线；超过硬限制时绝不把请求交给模型。
	if view.EstimatedTokens > m.budget.HardInputLimit {
		return nil, fmt.Errorf("%w: estimated %d tokens, hard limit %d", ErrContextBudgetExceeded, view.EstimatedTokens, m.budget.HardInputLimit)
	}
	return view, nil
}

// messagesForView 根据 active checkpoint 选择原始消息读取起点。
func (m *ContextManager) messagesForView(ctx context.Context, sessionID string, active *SummarySnapshot) ([]StoredMessage, error) {
	if active == nil || active.CoveredThroughMessageID == "" {
		return m.store.ListMessages(ctx, sessionID)
	}
	return m.store.ListMessagesAfter(ctx, sessionID, active.CoveredThroughMessageID)
}

// syncHistory 把旧 MessageManager 的新增部分转换为持久化消息。
// 这是迁移期兼容层：Store 是权威来源，MessageManager 只是当前进程缓存。
func (m *ContextManager) syncHistory(ctx context.Context, sessionID string, history []message.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	start, initialized := m.syncedCount[sessionID]
	if !initialized {
		existing, err := m.store.ListMessages(ctx, sessionID)
		if err != nil {
			return err
		}
		if len(existing) > len(history) {
			return errors.New("stored message history is longer than resumed history")
		}
		for index := range existing {
			if existing[index].Role != history[index].Role || existing[index].Content != history[index].Content {
				return fmt.Errorf("resumed history differs from transcript at message %d", index+1)
			}
		}
		start = len(existing)
		m.syncedCount[sessionID] = start
		turn := 0
		for _, item := range existing {
			if item.Role == message.USER {
				turn++
			}
		}
		if turn == 0 && len(existing) > 0 {
			turn = 1
		}
		m.turnCount[sessionID] = turn
	}
	if start > len(history) {
		return errors.New("message history shrank during session")
	}
	turn := m.turnCount[sessionID]
	for index := start; index < len(history); index++ {
		item := history[index]
		// 每条 user 消息开启一个新 Turn；之后的工具调用、工具结果和最终回复共享该 TurnID。
		if item.Role == message.USER {
			turn++
		}
		if turn == 0 {
			turn = 1
		}
		stored := fromMessage(item, sessionID, index+1, turn)
		if err := m.store.AppendMessage(ctx, stored); err != nil {
			return err
		}
		m.syncedCount[sessionID] = index + 1
	}
	m.turnCount[sessionID] = turn
	return nil
}

// SyncHistory 在一次 Agent Run 结束后立即持久化最终 assistant 回复。
func (m *ContextManager) SyncHistory(ctx context.Context, sessionID string, history []message.Message) error {
	if !validIdentifier(sessionID) {
		return ErrInvalidIdentifier
	}
	return m.syncHistory(ctx, sessionID, history)
}

// fromMessage 为旧消息生成稳定的 MessageID 和 TurnID，并识别最终 assistant 回复。
func fromMessage(item message.Message, sessionID string, index, turn int) StoredMessage {
	stored := StoredMessage{
		ID: fmt.Sprintf("message-%06d", index), SessionID: sessionID,
		TurnID: fmt.Sprintf("turn-%06d", turn), Role: item.Role, Content: item.Content, TurnStatus: TurnOpen,
	}
	for _, use := range item.ToolUses {
		stored.ToolUses = append(stored.ToolUses, StoredToolUse{ToolUseID: use.ToolUseID, ToolName: use.ToolName, Arguments: use.Arguments})
	}
	for _, result := range item.ToolResults {
		stored.ToolResults = append(stored.ToolResults, StoredToolResult{ToolUseID: result.ToolUseID, Content: result.Content, IsError: result.IsError, State: ResultFull})
	}
	// 没有 ToolUse 的 assistant 消息表示本轮最终回复，至此整个 Turn 才允许压缩。
	if item.Role == message.ASSISTANT && len(item.ToolUses) == 0 {
		stored.TurnStatus = TurnComplete
	}
	return stored
}

// renderView 把持久化类型转换回模型协议类型，并在最前面注入已生效的任务摘要。
func (m *ContextManager) renderView(systemPrompt string, active *SummarySnapshot, stored []StoredMessage, tools []*tool.ToolSchema, reports []LayerReport) *ContextView {
	var messages []message.Message
	if active != nil && active.Content != "" {
		// 边界提示强调摘要不是精确原文，需要细节时必须重新读取文件或 Artifact。
		messages = append(messages, message.Message{Role: message.USER, Content: "[compacted task state]\n" + active.Content + "\n[context boundary: read files or artifacts for exact details]"})
	}
	for _, item := range stored {
		converted := message.Message{Role: item.Role, Content: item.Content}
		for _, use := range item.ToolUses {
			converted.ToolUses = append(converted.ToolUses, message.ToolUseBlock{ToolUseID: use.ToolUseID, ToolName: use.ToolName, Arguments: use.Arguments})
		}
		for _, result := range item.ToolResults {
			converted.ToolResults = append(converted.ToolResults, message.ToolResultBlock{ToolUseID: result.ToolUseID, Content: result.Content, IsError: result.IsError})
		}
		messages = append(messages, converted)
	}
	estimated := m.estimator.EstimateText(m.model.ModelName, systemPrompt) + m.estimator.EstimateMessages(m.model.ModelName, messages) + m.estimator.EstimateTools(m.model.ModelName, tools)
	return &ContextView{SystemPrompt: systemPrompt, Messages: messages, Tools: tools, EstimatedTokens: estimated, Budget: m.budget, AppliedLayers: reports}
}

// activePaths 从工具参数中提取本轮真实操作过的文件路径，用于加载路径级规则。
func activePaths(messages []StoredMessage) []string {
	var paths []string
	for _, item := range messages {
		for _, use := range item.ToolUses {
			for key, value := range use.Arguments {
				lower := strings.ToLower(key)
				if !strings.Contains(lower, "path") && !strings.Contains(lower, "file") && !strings.Contains(lower, "directory") {
					continue
				}
				if path, ok := value.(string); ok && path != "" {
					paths = append(paths, filepath.Clean(path))
				}
			}
		}
	}
	return paths
}

// activeToolNames 保留当前历史中已经使用过的工具，避免下一轮突然丢失其 schema。
func activeToolNames(messages []StoredMessage) []string {
	seen := make(map[string]bool)
	var names []string
	for _, item := range messages {
		for _, use := range item.ToolUses {
			if !seen[use.ToolName] {
				seen[use.ToolName] = true
				names = append(names, use.ToolName)
			}
		}
	}
	return names
}

// appendRules 把持久规则重新注入 System Prompt；规则不依赖有损摘要长期保存。
func appendRules(systemPrompt string, rules []LoadedRule) string {
	var builder strings.Builder
	builder.WriteString(systemPrompt)
	for _, rule := range rules {
		builder.WriteString("\n\n[project context: ")
		builder.WriteString(rule.Path)
		builder.WriteString("]\n")
		builder.WriteString(rule.Content)
	}
	return builder.String()
}
