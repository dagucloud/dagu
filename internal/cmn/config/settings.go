// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Setting sources reported by Settings. An environment source is reported as
// SourceEnvPrefix followed by the variable name, such as "env:DAGU_PORT".
const (
	SourceDefault   = "default"
	SourceFile      = "file"
	SourceEnvPrefix = "env:"
)

// MaskedValue replaces the value of a secret setting that is set.
const MaskedValue = "********"

// Setting is one configuration key with its value and the source
// that supplied it.
type Setting struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Source string `json:"source"`
}

// Settings reports every key in the environment binding table, in table
// order, with the value read from its source before Dagu applies its own
// defaults. The exception is auth.mode, which reports the mode cfg runs in.
// Secret values are masked. cfg is the Config that Load returned.
func (l *ConfigLoader) Settings(cfg *Config) []Setting {
	prefix := strings.ToUpper(AppSlug) + "_"

	type keyInfo struct {
		envs   []string
		secret bool
	}
	var keys []string
	infos := make(map[string]*keyInfo)
	for _, b := range envBindings {
		if b.requires != SectionNone && !l.requires(b.requires) {
			continue
		}
		info, ok := infos[b.key]
		if !ok {
			// AutomaticEnv reads DAGU_<KEY> before any bound variable.
			info = &keyInfo{envs: []string{prefix + strings.ToUpper(b.key)}}
			infos[b.key] = info
			keys = append(keys, b.key)
		}
		info.envs = append(info.envs, prefix+b.env)
		info.secret = info.secret || b.secret
	}

	settings := make([]Setting, 0, len(keys))
	for _, key := range keys {
		info := infos[key]
		value := formatSettingValue(l.v.Get(key))
		// Load picks a mode when none or an invalid one is configured, and
		// whether auth is on is what this report is most often read for.
		if key == "auth.mode" && cfg.Server.Auth.Mode != "" {
			value = string(cfg.Server.Auth.Mode)
		}
		if info.secret && value != "" {
			value = MaskedValue
		}
		settings = append(settings, Setting{
			Key:    key,
			Value:  value,
			Source: l.settingSource(key, info.envs),
		})
	}
	return settings
}

// settingSource follows Viper's precedence: the first variable in envs that
// is set and non-empty, then the config file, then the default.
func (l *ConfigLoader) settingSource(key string, envs []string) string {
	for _, env := range envs {
		if os.Getenv(env) != "" {
			return SourceEnvPrefix + env
		}
	}
	if l.v.InConfig(key) {
		return SourceFile
	}
	return SourceDefault
}

func formatSettingValue(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case []string:
		return strings.Join(v, ",")
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = formatSettingValue(item)
		}
		return strings.Join(parts, ",")
	case map[string]any:
		if data, err := json.Marshal(v); err == nil {
			return string(data)
		}
	}
	return fmt.Sprint(value)
}
