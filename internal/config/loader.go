package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return filepath.Join(home, ".mycode", "config.yaml"), nil
}

func Load(warnings io.Writer) (Config, error) {
	path, err := DefaultPath()
	if err != nil {
		return Config{}, err
	}
	return LoadFile(path, warnings)
}

func LoadFile(path string, warnings io.Writer) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}

	var result Config
	if err := yaml.Unmarshal(data, &result); err != nil {
		return Config{}, fmt.Errorf("decode config %s: %w", path, err)
	}
	result.applyDefaults()
	if err := applyEnvironment(&result); err != nil {
		return Config{}, fmt.Errorf("load config %s: %w", path, err)
	}
	result.applyDefaults()
	if err := result.validate(); err != nil {
		return Config{}, fmt.Errorf("validate config %s: %w", path, err)
	}

	if info, statErr := os.Stat(path); statErr == nil && info.Mode().Perm()&0o077 != 0 && warnings != nil {
		fmt.Fprintf(warnings, "warning: config %s is readable by other users; run chmod 600 %s\n", path, path)
	}
	return result, nil
}

func applyEnvironment(config *Config) error {
	config.Model.Protocol = envString(config.Model.Protocol, "MYCODE_PROTOCOL")
	config.Model.BaseURL = envString(config.Model.BaseURL, "MYCODE_BASE_URL", "ANTHROPIC_BASE_URL")
	config.Model.APIKey = envString(config.Model.APIKey, "MYCODE_API_KEY", "ANTHROPIC_API_KEY")
	config.Model.Name = envString(config.Model.Name, "MYCODE_MODEL")
	config.Summary.Model = envString(config.Summary.Model, "MYCODE_SUMMARY_MODEL")
	config.Summary.BaseURL = envString(config.Summary.BaseURL, "MYCODE_SUMMARY_BASE_URL")
	config.Summary.APIKey = envString(config.Summary.APIKey, "MYCODE_SUMMARY_API_KEY")

	integerOverrides := []struct {
		name        string
		destination *int
	}{
		{name: "MYCODE_MAX_TOKENS", destination: &config.Model.MaxTokens},
		{name: "MYCODE_CONTEXT_WINDOW", destination: &config.Context.Window},
		{name: "MYCODE_MAX_OUTPUT_TOKENS", destination: &config.Context.OutputReserve},
	}
	for _, override := range integerOverrides {
		value := strings.TrimSpace(os.Getenv(override.name))
		if value == "" {
			continue
		}
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed <= 0 {
			return fmt.Errorf("environment variable %s has invalid positive integer %q", override.name, value)
		}
		*override.destination = parsed
	}
	return nil
}

func envString(fallback string, names ...string) string {
	result := fallback
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			result = value
		}
	}
	return result
}
