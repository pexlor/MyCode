package permission

type PermissionDecision string

const (
	Allow   PermissionDecision = "allow"
	Deny    PermissionDecision = "deny"
	Confirm PermissionDecision = "confirm"
)

type PermissionResult struct {
	Decision PermissionDecision
	Reason   string
}

func (r PermissionResult) Allowed() bool { return r.Decision == Allow }
