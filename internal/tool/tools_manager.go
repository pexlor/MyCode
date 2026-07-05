package tool

type ToolsManager struct {
	tools map[string]Tool
}

func NewToolsManager() *ToolsManager {
	return &ToolsManager{
		tools: make(map[string]Tool),
	}
}

func (m *ToolsManager) RegisterTool(t Tool) {
	m.tools[t.Name()] = t
}

func (m *ToolsManager) GetTool(name string) Tool {
	return m.tools[name]
}

func (m *ToolsManager) BuildAllSchemas() []map[string]any {
	schemas := make([]map[string]any, 0, len(m.tools))
	for _, t := range m.tools {
		base := t.Schema()
		schemas = append(schemas, base)
	}
	return schemas
}

func CreateDefaultTools() *ToolsManager {
	toolsManager := NewToolsManager()
	toolsManager.RegisterTool(&ReadFileTool{})
	return toolsManager
}
