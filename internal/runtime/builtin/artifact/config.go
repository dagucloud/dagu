// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package artifact

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/go-viper/mapstructure/v2"
	"github.com/google/jsonschema-go/jsonschema"
)

type config struct {
	Path        string `mapstructure:"path"`
	Content     string `mapstructure:"content"`
	Mode        string `mapstructure:"mode"`
	Format      string `mapstructure:"format"`
	Pattern     string `mapstructure:"pattern"`
	Overwrite   bool   `mapstructure:"overwrite"`
	Atomic      bool   `mapstructure:"atomic"`
	Recursive   bool   `mapstructure:"recursive"`
	IncludeDirs bool   `mapstructure:"include_dirs"`
	MaxBytes    int64  `mapstructure:"max_bytes"`

	hasContent bool
}

func defaultConfig() config {
	return config{
		Atomic: true,
	}
}

func decodeConfig(raw map[string]any, cfg *config) error {
	if raw == nil {
		raw = map[string]any{}
	}
	_, cfg.hasContent = raw["content"]
	if value, exists := raw["content"]; exists {
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%w: content must be a string", errConfig)
		}
	}

	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:           cfg,
		WeaklyTypedInput: true,
		ErrorUnused:      true,
		TagName:          "mapstructure",
	})
	if err != nil {
		return err
	}
	return decoder.Decode(raw)
}

func validateConfig(operation string, cfg config) error {
	switch operation {
	case opRead:
		if strings.TrimSpace(cfg.Path) == "" {
			return fmt.Errorf("%w: path is required for read", errConfig)
		}
	case opWrite:
		if strings.TrimSpace(cfg.Path) == "" {
			return fmt.Errorf("%w: path is required for write", errConfig)
		}
		if !cfg.hasContent {
			return fmt.Errorf("%w: content is required for write", errConfig)
		}
		if cfg.Overwrite && !cfg.Atomic {
			return fmt.Errorf("%w: overwrite requires atomic writes", errConfig)
		}
	case opList:
	default:
		return fmt.Errorf("%w: unsupported operation %q", errConfig, operation)
	}

	switch cfg.Format {
	case "", "raw", "json":
	default:
		return fmt.Errorf("%w: format must be raw or json", errConfig)
	}
	if cfg.MaxBytes < 0 {
		return fmt.Errorf("%w: max_bytes must be >= 0", errConfig)
	}
	if cfg.Pattern != "" && !doublestar.ValidatePattern(cfg.Pattern) {
		return fmt.Errorf("%w: invalid glob pattern %q", errConfig, cfg.Pattern)
	}
	if cfg.Mode != "" {
		if _, err := parseFileMode(cfg.Mode); err != nil {
			return err
		}
	}
	return nil
}

func parseFileMode(raw string) (os.FileMode, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "0o")
	raw = strings.TrimPrefix(raw, "0O")
	if raw == "" {
		return 0, fmt.Errorf("%w: mode must not be empty", errConfig)
	}
	parsed, err := strconv.ParseUint(raw, 8, 32)
	if err != nil {
		return 0, fmt.Errorf("%w: invalid mode %q", errConfig, raw)
	}
	return os.FileMode(parsed), nil
}

var configSchema = &jsonschema.Schema{
	Type:                 "object",
	AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	Properties: map[string]*jsonschema.Schema{
		"path":         {Type: "string", Description: "Relative artifact path. Must stay within the DAG-run artifact directory."},
		"content":      {Type: "string", Description: "Content to write for action: artifact.write."},
		"mode":         {Type: "string", Pattern: "^(0o|0O)?[0-7]{3,4}$", Description: "Octal file mode such as 0600 or 0644."},
		"format":       {Type: "string", Enum: []any{"raw", "json"}, Description: "Output format for artifact.read. Defaults to raw."},
		"pattern":      {Type: "string", Description: "Glob pattern for artifact.list, matched against slash-separated artifact paths."},
		"overwrite":    {Type: "boolean", Description: "Overwrite an existing artifact. Defaults to false."},
		"atomic":       {Type: "boolean", Description: "Use atomic replacement for overwriting artifact.write destinations. Defaults to true."},
		"recursive":    {Type: "boolean", Description: "Recurse into nested directories for artifact.list."},
		"include_dirs": {Type: "boolean", Description: "Include directories in artifact.list output."},
		"max_bytes":    {Type: "integer", Minimum: new(float64), Description: "Maximum bytes to read for artifact.read. Zero means no limit."},
	},
}

func init() {
	core.RegisterExecutorConfigSchema(executorType, configSchema)
}
