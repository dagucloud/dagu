// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package types

import (
	"fmt"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/goccy/go-yaml"
)

// LogOutputValue represents a log output configuration that can be unmarshaled from YAML.
// It accepts a string value that must be one of: "separate" or "merged".
// This type uses ir.LogOutputMode to avoid type duplication.
type LogOutputValue struct {
	mode ir.LogOutputMode
	set  bool // whether the value was explicitly set in YAML
}

// UnmarshalYAML implements BytesUnmarshaler for goccy/go-yaml.
func (l *LogOutputValue) UnmarshalYAML(data []byte) error {
	l.set = true

	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("log_output unmarshal error: %w", err)
	}

	switch v := raw.(type) {
	case string:
		value := strings.TrimSpace(strings.ToLower(v))
		switch value {
		case "separate", "":
			l.mode = ir.LogOutputSeparate
		case "merged":
			l.mode = ir.LogOutputMerged
		default:
			return fmt.Errorf("invalid log_output value: %q (must be 'separate' or 'merged')", v)
		}
		return nil

	case nil:
		l.set = false
		return nil

	default:
		return fmt.Errorf("log_output must be a string, got %T", v)
	}
}

// IsZero returns true if the value was not set in YAML.
func (l LogOutputValue) IsZero() bool {
	return !l.set
}

// Mode returns the log output mode.
// If the value was not set, it returns ir.LogOutputSeparate as the default.
func (l LogOutputValue) Mode() ir.LogOutputMode {
	if !l.set {
		return ir.LogOutputSeparate
	}
	return l.mode
}

// String returns the string representation of the log output mode.
func (l LogOutputValue) String() string {
	return string(l.Mode())
}

// IsMerged returns true if the log output mode is merged.
func (l LogOutputValue) IsMerged() bool {
	return l.mode == ir.LogOutputMerged
}

// IsSeparate returns true if the log output mode is separate.
func (l LogOutputValue) IsSeparate() bool {
	return !l.set || l.mode == ir.LogOutputSeparate
}
