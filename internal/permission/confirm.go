package permission

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
)

type ConfirmationDecision string

const (
	AllowOnce    ConfirmationDecision = "allow_once"
	AllowSession ConfirmationDecision = "allow_session"
	DenyRequest  ConfirmationDecision = "deny"
)

type Confirmer interface {
	Confirm(ctx context.Context, req PermissionRequest) (ConfirmationDecision, error)
}

type TerminalConfirmer struct {
	In  io.Reader
	Out io.Writer
}

func (c *TerminalConfirmer) Confirm(ctx context.Context, req PermissionRequest) (ConfirmationDecision, error) {
	if err := ctx.Err(); err != nil {
		return DenyRequest, err
	}
	if c == nil || c.In == nil || c.Out == nil {
		return DenyRequest, fmt.Errorf("terminal confirmer is not configured")
	}
	fmt.Fprintf(c.Out, "⚠ Agent 请求执行危险操作\n\nTool: %s\nCommand: %s\nReason: %s\n\n[y] Allow Once  [s] Allow Session  [n] Deny: ", req.ToolName, req.Command, strings.Join(req.RiskReasons, "; "))
	line, err := bufio.NewReader(c.In).ReadString('\n')
	if err != nil && err != io.EOF {
		return DenyRequest, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return AllowOnce, nil
	case "s", "session":
		return AllowSession, nil
	default:
		return DenyRequest, nil
	}
}
