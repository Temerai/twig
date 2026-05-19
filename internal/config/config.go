package config

import (
	"fmt"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

// Config holds the application configuration loaded from config.yaml.
type Config struct {
	DefaultTokenBudget int    `yaml:"default_token_budget"`
	CodebaseRoot       string `yaml:"codebase_root"`
}

// envVarPattern matches ${VAR_NAME} references for environment variable expansion.
var envVarPattern = regexp.MustCompile(`\$\{([^}]+)}`)

// expandEnvVars replaces all ${VAR_NAME} references in s with the corresponding
// environment variable values. Unset variables are replaced with the empty string.
func expandEnvVars(s string) string {
	return envVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		varName := envVarPattern.FindStringSubmatch(match)[1]
		return os.Getenv(varName)
	})
}

// applyDefaults fills in zero-value fields with sensible defaults.
func (c *Config) applyDefaults() {
	if c.DefaultTokenBudget == 0 {
		c.DefaultTokenBudget = 4000
	}
	if c.CodebaseRoot == "" {
		c.CodebaseRoot = "./"
	}
}

// Load reads configuration from config.yaml in the working directory,
// expands environment variable references, and applies sensible defaults
// for any missing values.
func Load() (*Config, error) {
	data, err := os.ReadFile("config.yaml")
	if err != nil {
		if os.IsNotExist(err) {
			cfg := &Config{}
			cfg.applyDefaults()
			return cfg, nil
		}
		return nil, fmt.Errorf("reading config.yaml: %w", err)
	}

	// Expand environment variables before unmarshalling.
	expanded := expandEnvVars(string(data))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parsing config.yaml: %w", err)
	}

	cfg.applyDefaults()
	return &cfg, nil
}
