package permission

// PermissionRequest describes one tool invocation before it is executed.
type PermissionRequest struct {
	ToolName         string
	Action           string
	Arguments        map[string]any
	Command          string
	WorkingDirectory string
	ResolvedPaths    []string
	RiskLevel        RiskLevel
	RiskReasons      []string
}

// ToolPermission describes the capabilities granted to a tool.
type ToolPermission struct {
	ReadOnly       bool     `yaml:"read_only"`
	CanWrite       bool     `yaml:"can_write"`
	CanDelete      bool     `yaml:"can_delete"`
	AllowedPaths   []string `yaml:"allowed_paths"`
	DeniedPaths    []string `yaml:"denied_paths"`
	RequireConfirm bool     `yaml:"require_confirm"`
}
