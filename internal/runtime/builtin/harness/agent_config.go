// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/internal/core"
)

func agentConfigFromBuiltinHarnessConfig(cfg map[string]any) (*core.AgentStepConfig, error) {
	cfg = core.NormalizeBuiltinAgentHarnessConfig(cfg)
	if err := core.ValidateBuiltinAgentHarnessConfig(cfg); err != nil {
		return nil, fmt.Errorf("harness: %w", err)
	}

	agentCfg := &core.AgentStepConfig{
		SafeMode:      true,
		MaxIterations: 50,
	}

	var err error
	if agentCfg.Model, err = optionalString(cfg, "model"); err != nil {
		return nil, err
	}
	if agentCfg.Soul, err = optionalString(cfg, "soul"); err != nil {
		return nil, err
	}
	if agentCfg.MaxIterations, err = optionalPositiveInt(cfg, "max_iterations", agentCfg.MaxIterations); err != nil {
		return nil, err
	}
	if agentCfg.SafeMode, err = optionalBool(cfg, "safe_mode", agentCfg.SafeMode); err != nil {
		return nil, err
	}
	if agentCfg.Skills, err = optionalStringSlice(cfg, "skills"); err != nil {
		return nil, err
	}
	if agentCfg.Tools, err = optionalAgentTools(cfg["tools"]); err != nil {
		return nil, err
	}
	if agentCfg.Memory, err = optionalAgentMemory(cfg["memory"]); err != nil {
		return nil, err
	}
	if agentCfg.WebSearch, err = optionalWebSearch(cfg["web_search"]); err != nil {
		return nil, err
	}

	return agentCfg, nil
}

func optionalString(cfg map[string]any, key string) (string, error) {
	raw, ok := cfg[key]
	if !ok || raw == nil {
		return "", nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("harness: builtin provider field %q must be a string", key)
	}
	return strings.TrimSpace(value), nil
}

func optionalBool(cfg map[string]any, key string, fallback bool) (bool, error) {
	raw, ok := cfg[key]
	if !ok || raw == nil {
		return fallback, nil
	}
	value, ok := raw.(bool)
	if !ok {
		return false, fmt.Errorf("harness: builtin provider field %q must be a bool", key)
	}
	return value, nil
}

func optionalPositiveInt(cfg map[string]any, key string, fallback int) (int, error) {
	raw, ok := cfg[key]
	if !ok || raw == nil {
		return fallback, nil
	}
	value, err := intFromValue(raw)
	if err != nil {
		return 0, fmt.Errorf("harness: builtin provider field %q must be an integer", key)
	}
	if value < 1 {
		return 0, fmt.Errorf("harness: builtin provider field %q must be at least 1", key)
	}
	return value, nil
}

func optionalStringSlice(cfg map[string]any, key string) ([]string, error) {
	raw, ok := cfg[key]
	if !ok || raw == nil {
		return nil, nil
	}
	return stringSliceFromValue(raw, fmt.Sprintf("harness: builtin provider field %q", key))
}

func optionalAgentTools(raw any) (*core.AgentToolsConfig, error) {
	if raw == nil {
		return nil, nil
	}
	cfg, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("harness: builtin provider field %q must be an object", "tools")
	}
	for key := range cfg {
		switch key {
		case "enabled", "bash_policy":
		default:
			return nil, fmt.Errorf("harness: unsupported builtin provider field %q", "tools."+key)
		}
	}

	tools := &core.AgentToolsConfig{}
	var err error
	tools.Enabled, err = optionalStringSlice(cfg, "enabled")
	if err != nil {
		return nil, err
	}
	tools.BashPolicy, err = optionalBashPolicy(cfg["bash_policy"])
	if err != nil {
		return nil, err
	}
	return tools, nil
}

func optionalBashPolicy(raw any) (*core.AgentBashPolicy, error) {
	if raw == nil {
		return nil, nil
	}
	cfg, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("harness: builtin provider field %q must be an object", "tools.bash_policy")
	}
	for key := range cfg {
		switch key {
		case "default_behavior", "deny_behavior", "rules":
		default:
			return nil, fmt.Errorf("harness: unsupported builtin provider field %q", "tools.bash_policy."+key)
		}
	}

	policy := &core.AgentBashPolicy{}
	var err error
	policy.DefaultBehavior, err = optionalString(cfg, "default_behavior")
	if err != nil {
		return nil, err
	}
	policy.DenyBehavior, err = optionalString(cfg, "deny_behavior")
	if err != nil {
		return nil, err
	}
	policy.Rules, err = optionalBashRules(cfg["rules"])
	if err != nil {
		return nil, err
	}
	return policy, nil
}

func optionalBashRules(raw any) ([]core.AgentBashRule, error) {
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("harness: builtin provider field %q must be an array", "tools.bash_policy.rules")
	}

	rules := make([]core.AgentBashRule, 0, len(items))
	for i, item := range items {
		cfg, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("harness: builtin provider field tools.bash_policy.rules[%d] must be an object", i)
		}
		for key := range cfg {
			switch key {
			case "name", "pattern", "action":
			default:
				return nil, fmt.Errorf("harness: unsupported builtin provider field %q", fmt.Sprintf("tools.bash_policy.rules[%d].%s", i, key))
			}
		}
		pattern, err := optionalString(cfg, "pattern")
		if err != nil {
			return nil, err
		}
		action, err := optionalString(cfg, "action")
		if err != nil {
			return nil, err
		}
		if pattern == "" {
			return nil, fmt.Errorf("harness: builtin provider field tools.bash_policy.rules[%d].pattern is required", i)
		}
		if action == "" {
			return nil, fmt.Errorf("harness: builtin provider field tools.bash_policy.rules[%d].action is required", i)
		}
		name, err := optionalString(cfg, "name")
		if err != nil {
			return nil, err
		}
		rules = append(rules, core.AgentBashRule{
			Name:    name,
			Pattern: pattern,
			Action:  action,
		})
	}
	return rules, nil
}

func optionalAgentMemory(raw any) (*core.AgentMemoryConfig, error) {
	if raw == nil {
		return nil, nil
	}
	cfg, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("harness: builtin provider field %q must be an object", "memory")
	}
	for key := range cfg {
		if key != "enabled" {
			return nil, fmt.Errorf("harness: unsupported builtin provider field %q", "memory."+key)
		}
	}
	enabled, err := optionalBool(cfg, "enabled", false)
	if err != nil {
		return nil, err
	}
	return &core.AgentMemoryConfig{Enabled: enabled}, nil
}

func optionalWebSearch(raw any) (*core.WebSearchConfig, error) {
	if raw == nil {
		return nil, nil
	}
	cfg, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("harness: builtin provider field %q must be an object", "web_search")
	}
	for key := range cfg {
		switch key {
		case "enabled", "max_uses", "allowed_domains", "blocked_domains", "user_location":
		default:
			return nil, fmt.Errorf("harness: unsupported builtin provider field %q", "web_search."+key)
		}
	}

	enabled, err := optionalBool(cfg, "enabled", false)
	if err != nil {
		return nil, err
	}
	webSearch := &core.WebSearchConfig{Enabled: enabled}
	if rawMaxUses, ok := cfg["max_uses"]; ok && rawMaxUses != nil {
		maxUses, err := intFromValue(rawMaxUses)
		if err != nil {
			return nil, fmt.Errorf("harness: builtin provider field %q must be an integer", "web_search.max_uses")
		}
		webSearch.MaxUses = &maxUses
	}
	webSearch.AllowedDomains, err = optionalStringSlice(cfg, "allowed_domains")
	if err != nil {
		return nil, err
	}
	webSearch.BlockedDomains, err = optionalStringSlice(cfg, "blocked_domains")
	if err != nil {
		return nil, err
	}
	webSearch.UserLocation, err = optionalWebSearchUserLocation(cfg["user_location"])
	if err != nil {
		return nil, err
	}
	return webSearch, nil
}

func optionalWebSearchUserLocation(raw any) (*core.WebSearchUserLocation, error) {
	if raw == nil {
		return nil, nil
	}
	cfg, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("harness: builtin provider field %q must be an object", "web_search.user_location")
	}
	for key := range cfg {
		switch key {
		case "city", "region", "country", "timezone":
		default:
			return nil, fmt.Errorf("harness: unsupported builtin provider field %q", "web_search.user_location."+key)
		}
	}
	city, err := optionalString(cfg, "city")
	if err != nil {
		return nil, err
	}
	region, err := optionalString(cfg, "region")
	if err != nil {
		return nil, err
	}
	country, err := optionalString(cfg, "country")
	if err != nil {
		return nil, err
	}
	timezone, err := optionalString(cfg, "timezone")
	if err != nil {
		return nil, err
	}
	return &core.WebSearchUserLocation{
		City:     city,
		Region:   region,
		Country:  country,
		Timezone: timezone,
	}, nil
}

func stringSliceFromValue(raw any, field string) ([]string, error) {
	switch v := raw.(type) {
	case []string:
		return append([]string(nil), v...), nil
	case []any:
		values := make([]string, len(v))
		for i := range v {
			text, ok := v[i].(string)
			if !ok {
				return nil, fmt.Errorf("%s[%d] must be a string", field, i)
			}
			values[i] = strings.TrimSpace(text)
		}
		return values, nil
	default:
		return nil, fmt.Errorf("%s must be an array of strings", field)
	}
}

func intFromValue(raw any) (int, error) {
	switch v := raw.(type) {
	case int:
		return v, nil
	case int8:
		return int(v), nil
	case int16:
		return int(v), nil
	case int32:
		return int(v), nil
	case int64:
		maxInt := int64(^uint(0) >> 1)
		minInt := -maxInt - 1
		if v > maxInt || v < minInt {
			return 0, fmt.Errorf("integer out of range")
		}
		return int(v), nil
	case uint:
		if uint64(v) > uint64(^uint(0)>>1) {
			return 0, fmt.Errorf("integer out of range")
		}
		return int(v), nil
	case uint8:
		return int(v), nil
	case uint16:
		return int(v), nil
	case uint32:
		if uint64(v) > uint64(^uint(0)>>1) {
			return 0, fmt.Errorf("integer out of range")
		}
		return int(v), nil
	case uint64:
		if v > uint64(^uint(0)>>1) {
			return 0, fmt.Errorf("integer out of range")
		}
		return int(v), nil
	case float32:
		f := float64(v)
		maxInt := int64(^uint(0) >> 1)
		minInt := -maxInt - 1
		if f != math.Trunc(f) || f > float64(maxInt) || f < float64(minInt) {
			return 0, fmt.Errorf("not an integer")
		}
		return int(f), nil
	case float64:
		maxInt := int64(^uint(0) >> 1)
		minInt := -maxInt - 1
		if v != math.Trunc(v) || v > float64(maxInt) || v < float64(minInt) {
			return 0, fmt.Errorf("not an integer")
		}
		return int(v), nil
	case string:
		return strconv.Atoi(strings.TrimSpace(v))
	default:
		return 0, fmt.Errorf("unsupported integer type %T", raw)
	}
}
