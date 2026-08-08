// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package archive

import (
	"fmt"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/go-viper/mapstructure/v2"
	"github.com/google/jsonschema-go/jsonschema"
)

// Config contains runtime options for the archive executor.
type Config struct {
	Source           string   `mapstructure:"source"`
	Destination      string   `mapstructure:"destination"`
	Format           string   `mapstructure:"format"`
	CompressionLevel int      `mapstructure:"compression_level"`
	Password         string   `mapstructure:"password"`
	Overwrite        bool     `mapstructure:"overwrite"`
	PreservePaths    bool     `mapstructure:"preserve_paths"`
	StripComponents  int      `mapstructure:"strip_components"`
	Include          []string `mapstructure:"include"`
	Exclude          []string `mapstructure:"exclude"`
	DryRun           bool     `mapstructure:"dry_run"`
	Verbose          bool     `mapstructure:"verbose"`
	FollowSymlinks   bool     `mapstructure:"follow_symlinks"`
	VerifyIntegrity  bool     `mapstructure:"verify_integrity"`
	ContinueOnError  bool     `mapstructure:"continue_on_error"`
}

func defaultConfig() Config {
	return Config{
		PreservePaths:    true,
		CompressionLevel: -1,
	}
}

func decodeConfig(raw map[string]any, cfg *Config) error {
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:           cfg,
		WeaklyTypedInput: true,
		ErrorUnused:      false,
		TagName:          "mapstructure",
	})
	if err != nil {
		return err
	}
	return decoder.Decode(raw)
}

func validateConfig(operation string, cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("%w: missing configuration", ErrConfig)
	}
	switch operation {
	case opExtract, opList:
		if cfg.Source == "" {
			return fmt.Errorf("%w: source is required for %s", ErrConfig, operation)
		}
	case opCreate:
		if cfg.Source == "" {
			return fmt.Errorf("%w: source is required for %s", ErrConfig, operation)
		}
		if cfg.Destination == "" && !cfg.DryRun {
			return fmt.Errorf("%w: destination is required for %s", ErrConfig, operation)
		}
	}

	if cfg.StripComponents < 0 {
		return fmt.Errorf("%w: strip_components must be >= 0", ErrConfig)
	}

	if cfg.Password != "" && operation != opExtract && operation != opList {
		return fmt.Errorf("%w: password is only supported for extract/list operations", ErrConfig)
	}

	for _, pattern := range append(cfg.Include, cfg.Exclude...) {
		if pattern == "" {
			continue
		}
		if !doublestar.ValidatePattern(pattern) {
			return fmt.Errorf("%w: invalid glob pattern %q", ErrConfig, pattern)
		}
	}

	return nil
}

var configSchema = &jsonschema.Schema{
	Type:     "object",
	Required: []string{"source"},
	Properties: map[string]*jsonschema.Schema{
		"source":            {Type: "string", Description: "File or directory to archive/extract"},
		"destination":       {Type: "string", Description: "Archive file path (required for create)"},
		"format":            {Type: "string", Description: "Archive format (zip, tar, tar.gz, etc.)"},
		"compression_level": {Type: "integer", Description: "Compression level (-1 for default)"},
		"password":          {Type: "string", Description: "Password for extract/list only"},
		"overwrite":         {Type: "boolean", Description: "Overwrite existing files"},
		"preserve_paths":    {Type: "boolean", Description: "Preserve directory structure (default: true)"},
		"strip_components":  {Type: "integer", Minimum: new(float64(0)), Description: "Strip leading path components (must be >= 0)"},
		"include":           {Type: "array", Items: &jsonschema.Schema{Type: "string"}, Description: "Glob patterns to include"},
		"exclude":           {Type: "array", Items: &jsonschema.Schema{Type: "string"}, Description: "Glob patterns to exclude"},
		"dry_run":           {Type: "boolean", Description: "Simulate without making changes"},
		"verbose":           {Type: "boolean", Description: "Enable verbose output"},
		"follow_symlinks":   {Type: "boolean", Description: "Follow symbolic links"},
		"verify_integrity":  {Type: "boolean", Description: "Verify archive integrity"},
		"continue_on_error": {Type: "boolean", Description: "Continue on errors"},
	},
}

func init() {
	registry.RegisterExecutorConfigSchema("archive", configSchema)
}
