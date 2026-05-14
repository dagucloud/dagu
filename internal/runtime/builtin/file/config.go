// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

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
	Path           string `mapstructure:"path"`
	Source         string `mapstructure:"source"`
	Destination    string `mapstructure:"destination"`
	Content        string `mapstructure:"content"`
	Mode           string `mapstructure:"mode"`
	Format         string `mapstructure:"format"`
	Pattern        string `mapstructure:"pattern"`
	Overwrite      bool   `mapstructure:"overwrite"`
	CreateDirs     bool   `mapstructure:"create_dirs"`
	Atomic         bool   `mapstructure:"atomic"`
	Recursive      bool   `mapstructure:"recursive"`
	MissingOK      bool   `mapstructure:"missing_ok"`
	DryRun         bool   `mapstructure:"dry_run"`
	IncludeDirs    bool   `mapstructure:"include_dirs"`
	FollowSymlinks bool   `mapstructure:"follow_symlinks"`
	MaxBytes       int64  `mapstructure:"max_bytes"`

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
		ErrorUnused:      false,
		TagName:          "mapstructure",
	})
	if err != nil {
		return err
	}
	return decoder.Decode(raw)
}

func validateConfig(operation string, cfg config) error {
	switch operation {
	case opStat, opRead, opDelete, opMkdir, opList:
		if strings.TrimSpace(cfg.Path) == "" {
			return fmt.Errorf("%w: path is required for %s", errConfig, operation)
		}
	case opWrite:
		if strings.TrimSpace(cfg.Path) == "" {
			return fmt.Errorf("%w: path is required for %s", errConfig, operation)
		}
		if !cfg.hasContent {
			return fmt.Errorf("%w: content is required for write", errConfig)
		}
	case opCopy, opMove:
		if strings.TrimSpace(cfg.Source) == "" {
			return fmt.Errorf("%w: source is required for %s", errConfig, operation)
		}
		if strings.TrimSpace(cfg.Destination) == "" {
			return fmt.Errorf("%w: destination is required for %s", errConfig, operation)
		}
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
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"path":            {Type: "string", Description: "File or directory path used by stat, read, write, delete, mkdir, and list."},
		"source":          {Type: "string", Description: "Source path used by copy and move."},
		"destination":     {Type: "string", Description: "Destination path used by copy and move."},
		"content":         {Type: "string", Description: "Content to write for action: file.write."},
		"mode":            {Type: "string", Pattern: "^(0o|0O)?[0-7]{3,4}$", Description: "Octal file or directory mode such as 0600 or 0750."},
		"format":          {Type: "string", Enum: []any{"raw", "json"}, Description: "Output format for file.read. Defaults to raw."},
		"pattern":         {Type: "string", Description: "Glob pattern for file.list, matched against slash-separated relative paths."},
		"overwrite":       {Type: "boolean", Description: "Overwrite an existing destination. Defaults to false."},
		"create_dirs":     {Type: "boolean", Description: "Create missing parent directories for write, copy, and move."},
		"atomic":          {Type: "boolean", Description: "Use atomic replacement for overwriting file.write destinations. Defaults to true."},
		"recursive":       {Type: "boolean", Description: "Recurse into directories for list/copy or allow recursive delete."},
		"missing_ok":      {Type: "boolean", Description: "Succeed when the target path is missing for stat/delete."},
		"dry_run":         {Type: "boolean", Description: "Report what would happen without mutating files for write/copy/move/delete/mkdir."},
		"include_dirs":    {Type: "boolean", Description: "Include directories in file.list output."},
		"follow_symlinks": {Type: "boolean", Description: "Follow the top-level source symlink for stat/copy."},
		"max_bytes":       {Type: "integer", Minimum: new(float64(0)), Description: "Maximum bytes to read for file.read. Zero means no limit."},
	},
}

func init() {
	core.RegisterExecutorConfigSchema(executorType, configSchema)
}
