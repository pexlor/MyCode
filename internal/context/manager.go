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

type LayerReport struct {
	Layer        string
	BeforeTokens int
	AfterTokens  int
	AffectedIDs  []string
	Reason       string
}

type ContextView struct {
	SystemPrompt    string
	Messages        []message.Message
	Tools           []*tool.ToolSchema
	EstimatedTokens int
	Budget          ContextBudget
	AppliedLayers   []LayerReport
}

type BuildInput struct {
	SessionID      string
	CurrentTurnID  string
	SystemPrompt   string
	CurrentRequest string
	History        []message.Message
	AvailableTools []*tool.ToolSchema
}

type ContextManagerConfig struct {
	Store     ConversationStore
	Estimator TokenEstimator
	Policy    ContextPolicy
	Model     ModelContextSpec
	Workspace string
	Primary   Summarizer
	Fallback  Summarizer
}

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

	mu          sync.Mutex
	syncedCount map[string]int
	turnCount   map[string]int
}

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

func (m *ContextManager) Build(ctx context.Context, input BuildInput) (*ContextView, error) {
	if !validIdentifier(input.SessionID) {
		return nil, ErrInvalidIdentifier
	}
	if input.History != nil {
		if err := m.syncHistory(ctx, input.SessionID, input.History); err != nil {
			return nil, err
		}
	}
	active, err := m.store.ActiveSummary(ctx, input.SessionID)
	if err != nil {
		return nil, err
	}
	stored, err := m.messagesForView(ctx, input.SessionID, active)
	if err != nil {
		return nil, err
	}
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
	if afterOffload > m.budget.ToolHistoryLimit {
		before := afterOffload
		stored = m.evictor.Evict(stored, input.CurrentTurnID)
		after := toolResultTokens(m.estimator, m.model.ModelName, stored)
		reports = append(reports, LayerReport{Layer: "stale_result_evictor", BeforeTokens: before, AfterTokens: after, Reason: "tool history budget exceeded"})
	}

	rules, err := m.loader.LoadRules(activePaths(stored))
	if err != nil {
		return nil, err
	}
	systemPrompt := appendRules(input.SystemPrompt, rules)
	selectedTools := m.loader.SelectTools(input.CurrentRequest, activeToolNames(stored), input.AvailableTools)
	view := m.renderView(systemPrompt, active, stored, selectedTools, reports)
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
	if view.EstimatedTokens > m.budget.HardInputLimit {
		return nil, fmt.Errorf("%w: estimated %d tokens, hard limit %d", ErrContextBudgetExceeded, view.EstimatedTokens, m.budget.HardInputLimit)
	}
	return view, nil
}

func (m *ContextManager) messagesForView(ctx context.Context, sessionID string, active *SummarySnapshot) ([]StoredMessage, error) {
	if active == nil || active.CoveredThroughMessageID == "" {
		return m.store.ListMessages(ctx, sessionID)
	}
	return m.store.ListMessagesAfter(ctx, sessionID, active.CoveredThroughMessageID)
}

func (m *ContextManager) syncHistory(ctx context.Context, sessionID string, history []message.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	start := m.syncedCount[sessionID]
	if start > len(history) {
		return errors.New("message history shrank during session")
	}
	turn := m.turnCount[sessionID]
	for index := start; index < len(history); index++ {
		item := history[index]
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
	if item.Role == message.ASSISTANT && len(item.ToolUses) == 0 {
		stored.TurnStatus = TurnComplete
	}
	return stored
}

func (m *ContextManager) renderView(systemPrompt string, active *SummarySnapshot, stored []StoredMessage, tools []*tool.ToolSchema, reports []LayerReport) *ContextView {
	var messages []message.Message
	if active != nil && active.Content != "" {
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
