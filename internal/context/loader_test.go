package contextmanager

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"MyCode/internal/tool"
)

func TestDemandLoaderLoadsRootAndPathRules(t *testing.T) {
	workspace := t.TempDir()
	rootRule := filepath.Join(workspace, ".agent", "context.md")
	nestedRule := filepath.Join(workspace, "internal", "agent", ".agent", "context.md")
	if err := os.MkdirAll(filepath.Dir(rootRule), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rootRule, []byte("root rule"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(nestedRule), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nestedRule, []byte("nested rule"), 0o600); err != nil {
		t.Fatal(err)
	}
	loader := DemandLoader{Workspace: workspace}
	rules, err := loader.LoadRules([]string{filepath.Join(workspace, "internal", "agent", "agent.go")})
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{rules[0].Content, rules[1].Content}; !reflect.DeepEqual(got, []string{"root rule", "nested rule"}) {
		t.Fatalf("rules = %#v", got)
	}
}

func TestDemandLoaderSkipsPathOutsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	rootRule := filepath.Join(workspace, ".agent", "context.md")
	if err := os.MkdirAll(filepath.Dir(rootRule), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rootRule, []byte("root rule"), 0o600); err != nil {
		t.Fatal(err)
	}

	outside := t.TempDir()
	outsideRule := filepath.Join(outside, ".agent", "context.md")
	if err := os.MkdirAll(filepath.Dir(outsideRule), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outsideRule, []byte("outside rule"), 0o600); err != nil {
		t.Fatal(err)
	}

	loader := DemandLoader{Workspace: workspace}
	rules, err := loader.LoadRules([]string{filepath.Join(outside, "secret.go")})
	if err != nil {
		t.Fatalf("LoadRules() error = %v, want outside path to be skipped", err)
	}
	if got := []string{rules[0].Content}; !reflect.DeepEqual(got, []string{"root rule"}) {
		t.Fatalf("rules = %#v, want only workspace root rule", got)
	}
}

func TestDemandLoaderSelectsAllAvailableTools(t *testing.T) {
	loader := DemandLoader{}
	all := []*tool.ToolSchema{{Name: "ReadFile"}, {Name: "Grep"}, {Name: "Glob"}, {Name: "WriteFile"}, {Name: "EditFile"}, {Name: "Bash"}, {Name: "mcp_issue"}}
	selected := loader.SelectTools("查看你有什么 tools", nil, all)
	var names []string
	for _, schema := range selected {
		names = append(names, schema.Name)
	}
	want := []string{"ReadFile", "Grep", "Glob", "WriteFile", "EditFile", "Bash", "mcp_issue"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("selected tools = %#v, want %#v", names, want)
	}
}
