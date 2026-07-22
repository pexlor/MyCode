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

func TestDemandLoaderRejectsPathOutsideWorkspace(t *testing.T) {
	loader := DemandLoader{Workspace: t.TempDir()}
	if _, err := loader.LoadRules([]string{filepath.Join(t.TempDir(), "secret.go")}); err == nil {
		t.Fatal("expected path outside workspace to fail")
	}
}

func TestDemandLoaderSelectsDeterministicToolGroups(t *testing.T) {
	loader := DemandLoader{}
	all := []*tool.ToolSchema{{Name: "ReadFile"}, {Name: "Grep"}, {Name: "Glob"}, {Name: "WriteFile"}, {Name: "EditFile"}, {Name: "Bash"}, {Name: "mcp_issue"}}
	selected := loader.SelectTools("修改 auth.go 并运行测试", nil, all)
	var names []string
	for _, schema := range selected {
		names = append(names, schema.Name)
	}
	want := []string{"ReadFile", "Grep", "Glob", "WriteFile", "EditFile", "Bash"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("selected tools = %#v, want %#v", names, want)
	}
}
