// Package mcp implements the client side of the Model Context Protocol for
// stdio servers.
package mcp

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config intentionally follows the widely used mcpServers configuration shape.
type Config struct {
	Servers map[string]ServerConfig `yaml:"mcpServers"`
}

type ServerConfig struct {
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args"`
	Env     map[string]string `yaml:"env"`
}

func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return Config{}, fmt.Errorf("decode MCP config: %w", err)
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (c Config) Validate() error {
	if len(c.Servers) == 0 {
		return errors.New("MCP config has no mcpServers entries")
	}
	for name, server := range c.Servers {
		if strings.TrimSpace(name) == "" {
			return errors.New("MCP server name cannot be empty")
		}
		if strings.TrimSpace(server.Command) == "" {
			return fmt.Errorf("MCP server %q command cannot be empty", name)
		}
	}
	return nil
}
