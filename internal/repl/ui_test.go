package repl

import (
	"MyCode/internal/config"
	"bytes"
	"strings"
	"testing"
)

func TestModelParametersFromConfig(t *testing.T) {
	model := config.ModelConfig{
		Protocol:  "anthropic",
		BaseURL:   "https://api.example.com",
		APIKey:    "secret",
		Name:      "model-a",
		MaxTokens: 4096,
	}

	got := modelParameters(model)
	if got.Protocol != "anthropic" || got.BaseURL != "https://api.example.com" || got.APIKey != "secret" {
		t.Fatalf("connection parameters = %#v", got)
	}
	if got.ModelName != "model-a" || got.MaxToken != 4096 {
		t.Fatalf("model parameters = %#v", got)
	}
}

func TestPrintWelcomeUsesConfiguredModel(t *testing.T) {
	var output bytes.Buffer
	printWelcomeTo(&output, "model-a")

	if !strings.Contains(output.String(), "model: model-a") {
		t.Fatalf("output = %q", output.String())
	}
}
