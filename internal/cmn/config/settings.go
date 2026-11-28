// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"

	"github.com/spf13/viper"
)

// Setting sources reported by Settings. An environment source is reported as
// SourceEnvPrefix followed by the variable name, such as "env:DAGU_PORT".
const (
	SourceDefault   = string(SourceKindDefault)
	SourceFile      = string(SourceKindFile)
	SourceEnvPrefix = string(SourceKindEnv) + ":"
)

// ValueSource is where Load found the value of a configuration key.
type ValueSource struct {
	Kind SourceKind
	// Name is the config file path, the environment variable, or the flag,
	// such as "--coordinator.host", that supplied the value. It is empty for
	// a default.
	Name string
}

// SourceKind is the kind of place a configuration value came from.
type SourceKind string

// Source kinds, in the order Load gives them precedence, lowest first.
const (
	SourceKindDefault SourceKind = "default"
	SourceKindFile    SourceKind = "file"
	SourceKindEnv     SourceKind = "env"
	SourceKindFlag    SourceKind = "flag"
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
// defaults. A key that Load filled from a deprecated key or variable reports
// the deprecated value and its source instead. auth.mode reports the mode
// cfg runs in, and an unset peer.insecure the value Load resolved. Keys for projects are left out while projects are off.
// Secret values are masked. cfg is the Config that Load returned.
func (l *ConfigLoader) Settings(cfg *Config) []Setting {
	type keyInfo struct {
		secret bool
	}
	var keys []string
	infos := make(map[string]*keyInfo)
	for _, b := range envBindings {
		if b.requires != SectionNone && !l.requires(b.requires) {
			continue
		}
		if b.projects && !cfg.ProjectsEnabled {
			continue
		}
		info, ok := infos[b.key]
		if !ok {
			info = &keyInfo{}
			infos[b.key] = info
			keys = append(keys, b.key)
		}
		info.secret = info.secret || b.secret
	}

	settings := make([]Setting, 0, len(keys))
	for _, key := range keys {
		info := infos[key]
		value := formatSettingValue(l.v.Get(key))
		source := l.settingSource(key)
		if legacy, ok := l.legacySettings[key]; ok {
			value, source = legacy.Value, legacy.Source
		}
		// Load picks a mode when none or an invalid one is configured, and
		// whether auth is on is what this report is most often read for.
		if key == "auth.mode" && cfg.Server.Auth.Mode != "" {
			value = string(cfg.Server.Auth.Mode)
		}
		// An unset peer.insecure resolves to true, with a TLS source still
		// selecting built-in TLS, so the row shows the value Load resolved.
		if key == "peer.insecure" && source == SourceDefault {
			value = strconv.FormatBool(cfg.Core.Peer.Insecure)
		}
		if info.secret && value != "" {
			value = MaskedValue
		}
		settings = append(settings, Setting{
			Key:    key,
			Value:  value,
			Source: source,
		})
	}
	return settings
}

// envNames returns the environment variables that set key, in the order
// Viper reads them: DAGU_<KEY> through AutomaticEnv, then each bound variable.
func envNames(key string) []string {
	prefix := strings.ToUpper(AppSlug) + "_"
	envs := []string{prefix + strings.ToUpper(key)}
	for _, b := range envBindings {
		if b.key == key {
			envs = append(envs, prefix+b.env)
		}
	}
	return envs
}

// reportLegacyKey records that Load filled key from the deprecated config
// key legacyKey, so Settings reports legacyKey's value and source for key.
func (l *ConfigLoader) reportLegacyKey(key, legacyKey string) {
	l.reportLegacy(key, formatSettingValue(l.v.Get(legacyKey)), l.settingSource(legacyKey))
}

// reportLegacy records value and source as what Settings reports for key.
// A later call for the same key replaces an earlier one, as the later
// deprecated value replaces the earlier one in Load.
func (l *ConfigLoader) reportLegacy(key, value, source string) {
	if l.legacySettings == nil {
		l.legacySettings = make(map[string]Setting)
	}
	l.legacySettings[key] = Setting{Key: key, Value: value, Source: source}
}

// settingSource returns the source of key as Settings reports it: default,
// file, or the kind and name of a variable or flag, such as "env:DAGU_PORT".
func (l *ConfigLoader) settingSource(key string) string {
	source := l.valueSource(key)
	switch source.Kind {
	case SourceKindDefault, SourceKindFile:
		return string(source.Kind)
	default:
		return string(source.Kind) + ":" + source.Name
	}
}

// valueSourceKeys returns the keys that Config.Sources reports: each key of
// PeerDef and coordinator.host.
func valueSourceKeys() []string {
	keys := []string{"coordinator.host"}
	peer := reflect.TypeFor[PeerDef]()
	for field := range peer.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("mapstructure"), ",")
		keys = append(keys, "peer."+name)
	}
	return keys
}

// valueSources returns what Config.Sources reports.
func (l *ConfigLoader) valueSources() map[string]ValueSource {
	keys := valueSourceKeys()
	sources := make(map[string]ValueSource, len(keys))
	for _, key := range keys {
		sources[key] = l.valueSource(key)
	}
	return sources
}

// setSourceFiles records, for valueSource, the config file Load read and
// the legacy admin.yaml merged over it, which may be empty.
func (l *ConfigLoader) setSourceFiles(configFile, adminFile string) {
	l.configFileUsed = configFile
	if adminFile == "" {
		return
	}
	l.adminConfig = viper.New()
	l.adminConfig.SetConfigFile(adminFile)
	// Load has merged this file already, so it reads.
	_ = l.adminConfig.ReadInConfig()
}

// valueSource follows Viper's precedence: a flag, then the first variable
// that sets key, then the config file whose value Load kept, then the
// default.
func (l *ConfigLoader) valueSource(key string) ValueSource {
	if flag, ok := l.flags[key]; ok {
		return ValueSource{Kind: SourceKindFlag, Name: flag}
	}
	for _, env := range envNames(key) {
		if os.Getenv(env) != "" {
			return ValueSource{Kind: SourceKindEnv, Name: env}
		}
	}
	if l.adminConfig != nil && l.adminConfig.InConfig(key) {
		return ValueSource{Kind: SourceKindFile, Name: l.adminConfig.ConfigFileUsed()}
	}
	if l.v.InConfig(key) {
		return ValueSource{Kind: SourceKindFile, Name: l.configFileUsed}
	}
	return ValueSource{Kind: SourceKindDefault}
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
