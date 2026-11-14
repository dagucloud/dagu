// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sysproxy

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Opening lines of the nested values in scutil's output.
const (
	scutilDictionary = "<dictionary> {"
	scutilArray      = "<array> {"
)

// parseScutil parses the dictionary that scutil --proxy prints. Nested
// dictionaries become maps, arrays become slices, and other values strings.
func parseScutil(out []byte) (map[string]any, error) {
	p := scutilParser{lines: strings.Split(string(out), "\n")}
	for p.pos < len(p.lines) {
		line := strings.TrimSpace(p.lines[p.pos])
		p.pos++
		if line == "" {
			continue
		}
		if line != scutilDictionary {
			return nil, fmt.Errorf("scutil output starts with %q, not a dictionary", line)
		}
		return p.block()
	}
	return nil, errors.New("scutil printed nothing")
}

type scutilParser struct {
	lines []string
	pos   int
}

// block reads key : value lines up to the closing brace of the current
// dictionary or array.
func (p *scutilParser) block() (map[string]any, error) {
	entries := map[string]any{}
	for p.pos < len(p.lines) {
		line := strings.TrimSpace(p.lines[p.pos])
		p.pos++
		switch line {
		case "":
			continue
		case "}":
			return entries, nil
		}
		key, value, ok := strings.Cut(line, " :")
		if !ok {
			return nil, fmt.Errorf("unexpected scutil line %q", line)
		}
		switch value = strings.TrimSpace(value); value {
		case scutilDictionary:
			nested, err := p.block()
			if err != nil {
				return nil, err
			}
			entries[key] = nested
		case scutilArray:
			nested, err := p.block()
			if err != nil {
				return nil, err
			}
			entries[key] = arrayItems(nested)
		default:
			entries[key] = value
		}
	}
	return nil, errors.New("scutil output ends inside a dictionary")
}

// arrayItems orders the entries of an array, which scutil keys by index.
func arrayItems(entries map[string]any) []any {
	items := make([]any, 0, len(entries))
	for i := 0; ; i++ {
		item, ok := entries[strconv.Itoa(i)]
		if !ok {
			return items
		}
		items = append(items, item)
	}
}

// macSystemConfig reads the macOS proxy settings that parseScutil returns.
func macSystemConfig(settings map[string]any) (systemConfig, []string) {
	var cfg systemConfig
	var warnings []string
	if scutilEnabled(settings, "ProxyAutoConfigEnable") {
		cfg.autoConfigURL = scutilString(settings, "ProxyAutoConfigURLString")
	}
	cfg.autoDetect = scutilEnabled(settings, "ProxyAutoDiscoveryEnable")
	for _, s := range []struct {
		prefix  string // of the setting keys, as in HTTPEnable
		kind    proxyKind
		servers *[]proxyServer
	}{
		{prefix: "HTTP", kind: httpProxy, servers: &cfg.static.http},
		// The secure web proxy is an HTTP proxy that https requests tunnel through.
		{prefix: "HTTPS", kind: httpProxy, servers: &cfg.static.https},
		{prefix: "SOCKS", kind: socks5Proxy, servers: &cfg.static.fallback},
	} {
		if !scutilEnabled(settings, s.prefix+"Enable") {
			continue
		}
		server, err := newProxyServer(s.kind, scutilString(settings, s.prefix+"Proxy"), scutilString(settings, s.prefix+"Port"))
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("ignored the macOS %s proxy: %v", s.prefix, err))
			continue
		}
		*s.servers = append(*s.servers, server)
	}
	if scutilEnabled(settings, "ExcludeSimpleHostnames") {
		cfg.static.bypass = append(cfg.static.bypass, plainHostCondition)
	}
	exceptions, _ := settings["ExceptionsList"].([]any)
	for _, exception := range exceptions {
		entry, _ := exception.(string)
		condition, err := bypassCondition(entry)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("ignored the macOS proxy exception %q: %v; requests to it use the proxy", entry, err))
			continue
		}
		cfg.static.bypass = append(cfg.static.bypass, condition)
	}
	return cfg, warnings
}

func scutilString(settings map[string]any, key string) string {
	value, _ := settings[key].(string)
	return value
}

func scutilEnabled(settings map[string]any, key string) bool {
	return scutilString(settings, key) == "1"
}
