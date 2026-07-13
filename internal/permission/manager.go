package permission

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type PermissionManager interface {
	Authorize(ctx context.Context, req PermissionRequest) (PermissionResult, error)
}

type Option func(*Manager)

func WithConfirmer(confirmer Confirmer) Option  { return func(m *Manager) { m.confirmer = confirmer } }
func WithAuditLogger(logger AuditLogger) Option { return func(m *Manager) { m.audit = logger } }
func WithCommandAnalyzer(analyzer *CommandAnalyzer) Option {
	return func(m *Manager) { m.analyzer = analyzer }
}

type Manager struct {
	policy    Policy
	paths     *PathValidator
	analyzer  *CommandAnalyzer
	confirmer Confirmer
	audit     AuditLogger

	mu             sync.RWMutex
	sessionAllowed map[string]struct{}
}

func NewManager(policy Policy, options ...Option) (*Manager, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	paths, err := NewPathValidator(policy.Workspace.Root, policy.ProtectedPaths)
	if err != nil {
		return nil, err
	}
	m := &Manager{
		policy:         policy,
		paths:          paths,
		analyzer:       NewCommandAnalyzer(),
		audit:          NopAuditLogger{},
		sessionAllowed: make(map[string]struct{}),
	}
	for _, option := range options {
		if option != nil {
			option(m)
		}
	}
	if m.analyzer == nil {
		return nil, errors.New("command analyzer cannot be nil")
	}
	if m.audit == nil {
		m.audit = NopAuditLogger{}
	}
	return m, nil
}

func (m *Manager) Authorize(ctx context.Context, req PermissionRequest) (result PermissionResult, err error) {
	// 权限防御
	start := time.Now()
	userDecision := ""
	defer func() {
		risk := req.RiskLevel.String()
		logErr := m.audit.Log(AuditEntry{
			Time: time.Now(), Tool: req.ToolName, Arguments: req.Arguments, Command: req.Command,
			Decision: result.Decision, Risk: risk, Reasons: append([]string(nil), req.RiskReasons...),
			User: userDecision, Duration: time.Since(start),
		})
		if err == nil && logErr != nil {
			err = fmt.Errorf("write permission audit log: %w", logErr)
		}
	}()

	if err = ctx.Err(); err != nil {
		result = PermissionResult{Decision: Deny, Reason: err.Error()}
		return
	}
	if strings.TrimSpace(req.ToolName) == "" {
		result = PermissionResult{Decision: Deny, Reason: "tool name is required"}
		return
	}
	if req.RiskLevel < Safe || req.RiskLevel > Critical {
		result = PermissionResult{Decision: Deny, Reason: "invalid risk level"}
		return
	}
	toolName := canonicalToolName(req.ToolName)
	if (toolName == "shell" || toolName == "bash") && strings.TrimSpace(req.Command) == "" {
		result = PermissionResult{Decision: Deny, Reason: "shell command is required"}
		return
	}
	toolPolicy, exists := m.policy.Tool(req.ToolName)
	toolDecision := m.policy.Default
	if exists {
		toolDecision = toolPolicy.Permission
	}
	if toolDecision == Deny {
		result = PermissionResult{Decision: Deny, Reason: "tool is denied by policy"}
		return
	}
	if !exists && m.policy.Default == Deny {
		result = PermissionResult{Decision: Deny, Reason: "tool is not present in the allowlist"}
		return
	}

	if reason := capabilityDenied(toolPolicy.ToolPermission, req.Action); reason != "" {
		result = PermissionResult{Decision: Deny, Reason: reason}
		return
	}
	if strings.TrimSpace(req.Command) != "" {
		analysis := m.analyzer.Analyze(req.Command, req.WorkingDirectory)
		req.RiskLevel = MaxRisk(req.RiskLevel, analysis.Risk)
		req.RiskReasons = appendUnique(req.RiskReasons, analysis.Reasons...)
		req.ResolvedPaths = appendUnique(req.ResolvedPaths, analysis.Paths...)
	}
	resolvedWorkingDirectory, workingDirErr := m.paths.Validate(".", req.WorkingDirectory)
	if workingDirErr != nil {
		req.RiskLevel = Critical
		req.RiskReasons = appendUnique(req.RiskReasons, workingDirErr.Error())
		result = PermissionResult{Decision: Deny, Reason: workingDirErr.Error()}
		return result, nil
	}
	req.WorkingDirectory = resolvedWorkingDirectory
	if req.Arguments != nil {
		req.Arguments["working_directory"] = resolvedWorkingDirectory
		if _, exists := req.Arguments["cwd"]; exists {
			req.Arguments["cwd"] = resolvedWorkingDirectory
		}
	}
	for i, requestedPath := range req.ResolvedPaths {
		resolved, pathErr := m.paths.Validate(requestedPath, req.WorkingDirectory)
		if pathErr != nil {
			req.RiskLevel = Critical
			req.RiskReasons = appendUnique(req.RiskReasons, pathErr.Error())
			result = PermissionResult{Decision: Deny, Reason: pathErr.Error()}
			return result, nil
		}
		req.ResolvedPaths[i] = resolved
		rewriteResolvedPath(req.Arguments, requestedPath, resolved)
		if reason := checkToolPathRules(m.paths.workspace, resolved, toolPolicy); reason != "" {
			result = PermissionResult{Decision: Deny, Reason: reason}
			return result, nil
		}
	}

	if req.RiskLevel == Critical {
		result = PermissionResult{Decision: Deny, Reason: joinReasons("critical operation", req.RiskReasons)}
		return
	}

	requiresConfirmation := req.RiskLevel == High || toolDecision == Confirm || toolPolicy.RequireConfirm
	if !requiresConfirmation {
		result = PermissionResult{Decision: Allow, Reason: joinReasons("allowed by policy", req.RiskReasons)}
		return
	}
	key := sessionKey(req)
	m.mu.RLock()
	_, sessionOK := m.sessionAllowed[key]
	m.mu.RUnlock()
	if sessionOK {
		userDecision = string(AllowSession)
		result = PermissionResult{Decision: Allow, Reason: "allowed for this session"}
		return
	}
	if m.confirmer == nil {
		result = PermissionResult{Decision: Confirm, Reason: joinReasons("user confirmation required", req.RiskReasons)}
		return
	}
	decision, confirmErr := m.confirmer.Confirm(ctx, req)
	if confirmErr != nil {
		result = PermissionResult{Decision: Deny, Reason: "confirmation failed"}
		err = confirmErr
		return
	}
	userDecision = string(decision)
	switch decision {
	case AllowOnce:
		result = PermissionResult{Decision: Allow, Reason: "user allowed once"}
	case AllowSession:
		m.mu.Lock()
		m.sessionAllowed[key] = struct{}{}
		m.mu.Unlock()
		result = PermissionResult{Decision: Allow, Reason: "user allowed for this session"}
	default:
		result = PermissionResult{Decision: Deny, Reason: "user denied operation"}
	}
	return
}

func rewriteResolvedPath(arguments map[string]any, original, resolved string) {
	for key, value := range arguments {
		switch typed := value.(type) {
		case string:
			if typed == original && isPathArgument(key) {
				arguments[key] = resolved
			}
		case []string:
			for index, path := range typed {
				if path == original {
					typed[index] = resolved
				}
			}
		case []any:
			for index, path := range typed {
				if stringPath, ok := path.(string); ok && stringPath == original {
					typed[index] = resolved
				}
			}
		}
	}
}

func isPathArgument(key string) bool {
	key = strings.ToLower(key)
	return strings.Contains(key, "path") || strings.Contains(key, "file") || strings.Contains(key, "directory")
}

func capabilityDenied(p ToolPermission, action string) string {
	action = strings.ToLower(strings.TrimSpace(action))
	hasCapabilities := p.ReadOnly || p.CanWrite || p.CanDelete
	if !hasCapabilities {
		return ""
	}
	if p.ReadOnly && (strings.Contains(action, "write") || strings.Contains(action, "delete") || strings.Contains(action, "remove")) {
		return "read-only tool cannot modify files"
	}
	if strings.Contains(action, "delete") || strings.Contains(action, "remove") {
		if !p.CanDelete {
			return "tool does not have delete permission"
		}
	} else if strings.Contains(action, "write") || strings.Contains(action, "create") || strings.Contains(action, "modify") {
		if !p.CanWrite {
			return "tool does not have write permission"
		}
	}
	return ""
}

func checkToolPathRules(workspace, path string, policy ToolPolicy) string {
	for _, raw := range policy.DeniedPaths {
		denied, err := resolvePolicyPath(workspace, raw)
		if err == nil && isWithin(denied, path) {
			return fmt.Sprintf("path %q is denied for this tool", path)
		}
	}
	if len(policy.AllowedPaths) == 0 {
		return ""
	}
	for _, raw := range policy.AllowedPaths {
		allowed, err := resolvePolicyPath(workspace, raw)
		if err == nil && isWithin(allowed, path) {
			return ""
		}
	}
	return fmt.Sprintf("path %q is outside the tool's allowed paths", path)
}

func resolvePolicyPath(workspace, path string) (string, error) {
	expanded, err := expandPath(path)
	if err != nil {
		return "", err
	}
	expanded = platformAbsolute(expanded, workspace)
	if !filepath.IsAbs(expanded) {
		expanded = filepath.Join(workspace, expanded)
	}
	return canonicalPath(expanded)
}

func sessionKey(req PermissionRequest) string {
	return canonicalToolName(req.ToolName) + "\x00" + strings.ToLower(strings.TrimSpace(req.Action))
}

func appendUnique(dst []string, values ...string) []string {
	seen := make(map[string]struct{}, len(dst))
	for _, value := range dst {
		seen[value] = struct{}{}
	}
	for _, value := range values {
		if _, ok := seen[value]; !ok && value != "" {
			dst = append(dst, value)
			seen[value] = struct{}{}
		}
	}
	return dst
}

func joinReasons(prefix string, reasons []string) string {
	if len(reasons) == 0 {
		return prefix
	}
	return prefix + ": " + strings.Join(reasons, "; ")
}
