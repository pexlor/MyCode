package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteAndEditFileTools(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "note.txt")
	write := (&WriteFileTool{}).Execute(context.Background(), map[string]any{"file_path": path, "content": "one\none\n"})
	if write.IsError {
		t.Fatal(write.Output)
	}
	edit := (&EditFileTool{}).Execute(context.Background(), map[string]any{"file_path": path, "old_string": "one", "new_string": "two"})
	if !edit.IsError {
		t.Fatal("expected ambiguous edit to fail")
	}
	edit = (&EditFileTool{}).Execute(context.Background(), map[string]any{"file_path": path, "old_string": "one", "new_string": "two", "replace_all": true})
	if edit.IsError {
		t.Fatal(edit.Output)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "two\ntwo\n" {
		t.Fatalf("file = %q, err = %v", data, err)
	}
}

func TestGrepAndGlobTools(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "keep.go"), []byte("package demo\nfunc Keep() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skip.txt"), []byte("nothing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	grep := (&GrepTool{}).Execute(context.Background(), map[string]any{"path": root, "pattern": "Keep"})
	if grep.IsError || !strings.Contains(grep.Output, "keep.go:2:func Keep") {
		t.Fatalf("grep = %#v", grep)
	}
	glob := (&GlobTool{}).Execute(context.Background(), map[string]any{"path": root, "pattern": "**/*.go"})
	if glob.IsError || !strings.Contains(glob.Output, "keep.go") {
		t.Fatalf("glob = %#v", glob)
	}
}

func TestDefaultToolsIncludesFilesystemTools(t *testing.T) {
	tools := CreateDefaultTools()
	for _, name := range []string{"WriteFile", "EditFile", "Grep", "Glob"} {
		if tools.GetTool(name) == nil {
			t.Fatalf("%s is not registered", name)
		}
	}
}
