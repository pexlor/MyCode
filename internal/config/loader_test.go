package config

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadFile(t *testing.T) {
	clearConfigEnvironment(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte("model:\n  protocol: anthropic\n  base_url: https://api.example.com\n  api_key: secret\n  name: model-a\n  max_tokens: 4096\ncontext:\n  window: 200000\n  output_reserve: 8192\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LoadFile(path, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Model.Name != "model-a" || got.Model.MaxTokens != 4096 || got.Context.Window != 200000 {
		t.Fatalf("config = %#v", got)
	}
}

func TestLoadFileAppliesDefaultsAndSummaryBaseURL(t *testing.T) {
	clearConfigEnvironment(t)
	path := writeValidConfig(t, 0o600, "summary:\n  model: summary-a\n  api_key: summary-secret\n")

	got, err := LoadFile(path, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Context.Window != DefaultContextWindow || got.Context.OutputReserve != DefaultOutputReserve {
		t.Fatalf("context defaults = %#v", got.Context)
	}
	if got.Summary.BaseURL != got.Model.BaseURL {
		t.Fatalf("summary base URL = %q, want %q", got.Summary.BaseURL, got.Model.BaseURL)
	}
}

func TestLoadFileErrorsIncludePath(t *testing.T) {
	clearConfigEnvironment(t)
	path := filepath.Join(t.TempDir(), "missing.yaml")
	if _, err := LoadFile(path, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("missing file error = %v", err)
	}
	if err := os.WriteFile(path, []byte("model: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "decode") || !strings.Contains(err.Error(), path) {
		t.Fatalf("decode error = %v", err)
	}
}

func TestLoadFileValidatesRequiredFields(t *testing.T) {
	clearConfigEnvironment(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("model:\n  protocol: anthropic\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadFile(path, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "model.base_url") {
		t.Fatalf("validation error = %v", err)
	}
}

func TestLoadFileWarnsForBroadPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are not enforced on Windows")
	}
	clearConfigEnvironment(t)
	path := writeValidConfig(t, 0o644, "")
	var warnings bytes.Buffer

	if _, err := LoadFile(path, &warnings); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(warnings.String(), "chmod 600") || !strings.Contains(warnings.String(), path) {
		t.Fatalf("warning = %q", warnings.String())
	}
}

func writeValidConfig(t *testing.T, mode os.FileMode, suffix string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := "model:\n  protocol: anthropic\n  base_url: https://api.example.com\n  api_key: secret\n  name: model-a\n" + suffix
	if err := os.WriteFile(path, []byte(data), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func clearConfigEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"MYCODE_PROTOCOL", "MYCODE_BASE_URL", "ANTHROPIC_BASE_URL",
		"MYCODE_API_KEY", "ANTHROPIC_API_KEY", "MYCODE_MODEL",
		"MYCODE_MAX_TOKENS", "MYCODE_SUMMARY_MODEL", "MYCODE_SUMMARY_BASE_URL",
		"MYCODE_SUMMARY_API_KEY", "MYCODE_CONTEXT_WINDOW", "MYCODE_MAX_OUTPUT_TOKENS",
	} {
		t.Setenv(name, "")
	}
}
