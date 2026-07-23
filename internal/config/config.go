package config

import (
	"fmt"
	"strings"
)

const (
	DefaultContextWindow = 128000
	DefaultOutputReserve = 8192
)

type Config struct {
	Model   ModelConfig   `yaml:"model"`
	Summary SummaryConfig `yaml:"summary"`
	Context ContextConfig `yaml:"context"`
}

type ModelConfig struct {
	Protocol  string `yaml:"protocol"`
	BaseURL   string `yaml:"base_url"`
	APIKey    string `yaml:"api_key"`
	Name      string `yaml:"name"`
	MaxTokens int    `yaml:"max_tokens"`
}

type SummaryConfig struct {
	Model   string `yaml:"model"`
	BaseURL string `yaml:"base_url"`
	APIKey  string `yaml:"api_key"`
}

type ContextConfig struct {
	Window        int `yaml:"window"`
	OutputReserve int `yaml:"output_reserve"`
}

func (c *Config) applyDefaults() {
	if c.Context.Window == 0 {
		c.Context.Window = DefaultContextWindow
	}
	if c.Context.OutputReserve == 0 {
		c.Context.OutputReserve = DefaultOutputReserve
	}
	if c.Summary.Model != "" && strings.TrimSpace(c.Summary.BaseURL) == "" {
		c.Summary.BaseURL = c.Model.BaseURL
	}
}

func (c Config) validate() error {
	required := []struct {
		name  string
		value string
	}{
		{name: "model.protocol", value: c.Model.Protocol},
		{name: "model.base_url", value: c.Model.BaseURL},
		{name: "model.api_key", value: c.Model.APIKey},
		{name: "model.name", value: c.Model.Name},
	}
	for _, field := range required {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%s is required", field.name)
		}
	}
	if c.Model.MaxTokens < 0 {
		return fmt.Errorf("model.max_tokens must be non-negative")
	}
	if c.Context.Window <= 0 {
		return fmt.Errorf("context.window must be positive")
	}
	if c.Context.OutputReserve <= 0 {
		return fmt.Errorf("context.output_reserve must be positive")
	}
	if c.Summary.Model != "" && strings.TrimSpace(c.Summary.APIKey) == "" {
		return fmt.Errorf("summary.api_key is required when summary.model is set")
	}
	return nil
}
