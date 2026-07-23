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
	if _, err := LoadFile(path, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "model:") {
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

func TestEnvironmentOverridesFile(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv("MYCODE_PROTOCOL", "openai-compat")
	t.Setenv("MYCODE_BASE_URL", "https://mycode.example.com")
	t.Setenv("ANTHROPIC_BASE_URL", "https://protocol.example.com")
	t.Setenv("MYCODE_API_KEY", "mycode-key")
	t.Setenv("ANTHROPIC_API_KEY", "protocol-key")
	t.Setenv("MYCODE_MODEL", "model-b")
	t.Setenv("MYCODE_MAX_TOKENS", "2048")
	t.Setenv("MYCODE_SUMMARY_MODEL", "summary-b")
	t.Setenv("MYCODE_SUMMARY_BASE_URL", "https://summary.example.com")
	t.Setenv("MYCODE_SUMMARY_API_KEY", "summary-key")
	t.Setenv("MYCODE_CONTEXT_WINDOW", "64000")
	t.Setenv("MYCODE_MAX_OUTPUT_TOKENS", "4096")

	got, err := LoadFile(writeValidConfig(t, 0o600, ""), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Model.BaseURL != "https://protocol.example.com" || got.Model.APIKey != "protocol-key" {
		t.Fatalf("protocol-specific precedence failed: %#v", got.Model)
	}
	if got.Model.Protocol != "openai-compat" || got.Model.Name != "model-b" || got.Model.MaxTokens != 2048 {
		t.Fatalf("model overrides = %#v", got.Model)
	}
	if got.Summary.Model != "summary-b" || got.Summary.BaseURL != "https://summary.example.com" || got.Summary.APIKey != "summary-key" {
		t.Fatalf("summary overrides = %#v", got.Summary)
	}
	if got.Context.Window != 64000 || got.Context.OutputReserve != 4096 {
		t.Fatalf("context overrides = %#v", got.Context)
	}
}

func TestInvalidEnvironmentIntegerIsAnError(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv("MYCODE_CONTEXT_WINDOW", "large")

	_, err := LoadFile(writeValidConfig(t, 0o600, ""), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "MYCODE_CONTEXT_WINDOW") || !strings.Contains(err.Error(), "large") {
		t.Fatalf("error = %v", err)
	}
}

func TestSummaryBaseURLFollowsOverriddenModelBaseURL(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv("ANTHROPIC_BASE_URL", "https://overridden.example.com")
	path := writeValidConfig(t, 0o600, "summary:\n  model: summary-a\n  api_key: summary-secret\n")

	got, err := LoadFile(path, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary.BaseURL != "https://overridden.example.com" {
		t.Fatalf("summary base URL = %q", got.Summary.BaseURL)
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
