// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Spec 093, "Configuration". The block ai sets the operational values of the
// gateway and of the models list. The coordinator, the server and a local process
// read it. The block has a closed schema. An unknown key, a wrong type, a value
// out of range and a contradiction of two keys stop Dagu at start. The error
// names the key, the expected type or range, and the value. The loader changes no
// value to make it fit. The block has no secret and no key that an environment
// variable can set: the file is the only source.

// AIBlock is the name of the block in the configuration file.
const AIBlock = "ai"

// aiProxyURLKey is the only key of the block that holds a string.
const aiProxyURLKey = "upstream.proxy_url"

const aiProxyURLExpected = "an http or https URL with no user information, or an empty string"

// AIConfig is the block ai after the checks. Each limit is below 2^31, so an
// int holds it on each platform.
type AIConfig struct {
	// ModelsRefreshHours is the period of the refresh of the models lists. Zero
	// stops the automatic refresh.
	ModelsRefreshHours int
	ModelsTimeoutSec   int
	Upstream           AIUpstreamConfig
	Relay              AIRelayConfig
	// Sources tells where each value came from: "file" or "default". The key is
	// the dotted name without the block name, such as relay.max_streams.
	Sources map[string]string
}

// AIUpstreamConfig is the block ai.upstream.
type AIUpstreamConfig struct {
	// ProxyURL is the proxy of the calls to the providers. Empty means a direct call.
	ProxyURL string
}

// AIRelayConfig is the block ai.relay. The coordinator sends the limits to the relay.
type AIRelayConfig struct {
	MaxRequestBytes          int
	MaxResponseBytes         int
	MaxHeaderBytes           int
	MaxStreamsPerRun         int
	MaxStreams               int
	ConnectTimeoutSec        int
	ResponseHeaderTimeoutSec int
	IdleTimeoutSec           int
	TotalTimeoutSec          int
}

// aiKey describes one key of the block. The table is the single source of the
// schema, the defaults, the ranges, the printed configuration and the reference.
type aiKey struct {
	// path is the dotted name without the block name.
	path string
	// integer is true for an integer key. The other key holds a string.
	integer     bool
	min, max    int
	def         int
	description string
	// field gives the field of an integer key.
	field func(*AIConfig) *int
}

// aiKeys lists the keys in the order of Spec 093.
var aiKeys = []aiKey{
	{path: "models_refresh_hours", integer: true, min: 0, max: 720, def: 24,
		description: "The period of the refresh of the models lists, in hours. Zero stops the automatic refresh.",
		field:       func(c *AIConfig) *int { return &c.ModelsRefreshHours }},
	{path: "models_timeout_sec", integer: true, min: 1, max: 300, def: 30,
		description: "The time limit of the read of one models list, in seconds.",
		field:       func(c *AIConfig) *int { return &c.ModelsTimeoutSec }},
	{path: aiProxyURLKey,
		description: "The proxy of the calls of the coordinator to the providers. An empty value means a direct call."},
	{path: "relay.max_request_bytes", integer: true, min: 1024, max: 268435456, def: 16777216,
		description: "The size limit of the body of one request. A larger body gives request_too_large.",
		field:       func(c *AIConfig) *int { return &c.Relay.MaxRequestBytes }},
	{path: "relay.max_response_bytes", integer: true, min: 1024, max: 1073741824, def: 268435456,
		description: "The size limit of one response. A larger response ends the stream with response_too_large.",
		field:       func(c *AIConfig) *int { return &c.Relay.MaxResponseBytes }},
	{path: "relay.max_header_bytes", integer: true, min: 1024, max: 1048576, def: 32768,
		description: "The size limit of the request headers that the relay forwards, all together.",
		field:       func(c *AIConfig) *int { return &c.Relay.MaxHeaderBytes }},
	{path: "relay.max_streams_per_run", integer: true, min: 1, max: 256, def: 8,
		description: "The most open streams of one run.",
		field:       func(c *AIConfig) *int { return &c.Relay.MaxStreamsPerRun }},
	{path: "relay.max_streams", integer: true, min: 1, max: 65536, def: 128,
		description: "The most open streams of the coordinator.",
		field:       func(c *AIConfig) *int { return &c.Relay.MaxStreams }},
	{path: "relay.connect_timeout_sec", integer: true, min: 1, max: 120, def: 10,
		description: "The time limit to connect to the provider, in seconds.",
		field:       func(c *AIConfig) *int { return &c.Relay.ConnectTimeoutSec }},
	{path: "relay.response_header_timeout_sec", integer: true, min: 1, max: 3600, def: 600,
		description: "The time limit to wait for the response head of the provider, in seconds.",
		field:       func(c *AIConfig) *int { return &c.Relay.ResponseHeaderTimeoutSec }},
	{path: "relay.idle_timeout_sec", integer: true, min: 1, max: 3600, def: 300,
		description: "A stream that moves no byte for this time ends, in seconds.",
		field:       func(c *AIConfig) *int { return &c.Relay.IdleTimeoutSec }},
	{path: "relay.total_timeout_sec", integer: true, min: 1, max: 86400, def: 3600,
		description: "The time limit of one request, in seconds.",
		field:       func(c *AIConfig) *int { return &c.Relay.TotalTimeoutSec }},
}

// expected describes the valid values of the key for an error message.
func (k aiKey) expected() string {
	if !k.integer {
		return aiProxyURLExpected
	}
	return fmt.Sprintf("an integer from %d to %d", k.min, k.max)
}

// AIKeyDoc documents one key of the block ai.
type AIKeyDoc struct {
	// Key is the dotted name without the block name, such as relay.max_streams.
	Key string
	// Type is "integer" or "string".
	Type    string
	Default string
	// Range tells the valid values.
	Range string
	// Min and Max are the limits of an integer key. They are zero for a string key.
	Min, Max    int
	Description string
}

// AIKeyDocs lists each key of the block with its type, its default and its range,
// in the order of Spec 093. The code is the single source of this table: the
// loader, the printed configuration and the reference read it.
func AIKeyDocs() []AIKeyDoc {
	docs := make([]AIKeyDoc, 0, len(aiKeys))
	for _, key := range aiKeys {
		doc := AIKeyDoc{Key: key.path, Type: "string", Default: "empty", Range: aiProxyURLExpected, Description: key.description}
		if key.integer {
			doc.Type = "integer"
			doc.Default = strconv.Itoa(key.def)
			doc.Range = fmt.Sprintf("%d to %d", key.min, key.max)
			doc.Min, doc.Max = key.min, key.max
		}
		docs = append(docs, doc)
	}
	return docs
}

// Values returns the value that applies for each key of the block, as text, for
// the printed configuration and for the log at start. The key is the dotted name
// without the block name. An empty proxy shows as "(empty)".
func (c AIConfig) Values() map[string]string {
	values := map[string]string{aiProxyURLKey: c.Upstream.ProxyURL}
	if c.Upstream.ProxyURL == "" {
		values[aiProxyURLKey] = "(empty)"
	}
	for _, key := range aiKeys {
		if key.integer {
			values[key.path] = strconv.Itoa(*key.field(&c))
		}
	}
	return values
}

func newAIConfig() AIConfig {
	cfg := AIConfig{Sources: map[string]string{}}
	for _, key := range aiKeys {
		if key.integer {
			*key.field(&cfg) = key.def
		}
		cfg.Sources[key.path] = "default"
	}
	return cfg
}

// ParseAIConfig checks the raw block and returns the typed configuration. raw is
// the value of the key ai, or nil when the file has none.
func ParseAIConfig(raw any) (AIConfig, error) {
	cfg := newAIConfig()
	if raw == nil {
		return cfg, nil
	}
	if err := parseAIBlock(&cfg, AIBlock, "", raw); err != nil {
		return cfg, err
	}
	if err := checkAIContradictions(cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// aiBlockKeys lists the names that a block accepts, with the expected value for
// the error messages. prefix is the path of the block in the table, with a final
// dot, or empty for the block ai.
func aiBlockKeys(prefix string) map[string]string {
	names := map[string]string{}
	for _, key := range aiKeys {
		rest, ok := strings.CutPrefix(key.path, prefix)
		if !ok {
			continue
		}
		if name, _, nested := strings.Cut(rest, "."); nested {
			names[name] = "an object"
		} else {
			names[name] = key.expected()
		}
	}
	return names
}

// parseAIBlock reads one block of the schema. name is the dotted name of the
// block, such as ai.relay.
func parseAIBlock(cfg *AIConfig, name, prefix string, raw any) error {
	block, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("config: key %q: expected an object, got %s", name, showConfigValue(raw))
	}
	valid := aiBlockKeys(prefix)
	for _, key := range sortedMapKeys(block) {
		if _, known := valid[key]; !known {
			return fmt.Errorf("config: unknown key %q in the block %s; valid keys are %s",
				name+"."+key, name, strings.Join(sortedMapKeys(valid), ", "))
		}
	}
	for _, key := range sortedMapKeys(block) {
		path := prefix + key
		if valid[key] == "an object" {
			if err := parseAIBlock(cfg, name+"."+key, path+".", block[key]); err != nil {
				return err
			}
			continue
		}
		if err := setAIKey(cfg, path, block[key]); err != nil {
			return err
		}
	}
	return nil
}

// setAIKey checks the value of one key and sets it.
func setAIKey(cfg *AIConfig, path string, value any) error {
	var spec aiKey
	for _, key := range aiKeys {
		if key.path == path {
			spec = key
		}
	}
	fail := func(shown any) error {
		return fmt.Errorf("config: key %q: expected %s, got %s", AIBlock+"."+path, spec.expected(), showConfigValue(shown))
	}
	if !spec.integer {
		text, ok := value.(string)
		if !ok {
			return fail(value)
		}
		proxy, err := parseAIProxyURL(text)
		if err != nil {
			return fail(proxy)
		}
		cfg.Upstream.ProxyURL = text
		cfg.Sources[path] = "file"
		return nil
	}
	number, ok := configInt(value)
	if !ok || number < int64(spec.min) || number > int64(spec.max) {
		return fail(value)
	}
	*spec.field(cfg) = int(number)
	cfg.Sources[path] = "file"
	return nil
}

// parseAIProxyURL checks a proxy URL. An empty string means a direct call. On an
// error it returns the text to show, with the user information hidden.
func parseAIProxyURL(text string) (shown string, err error) {
	if text == "" {
		return "", nil
	}
	u, parseErr := url.Parse(text)
	if parseErr != nil {
		return text, parseErr
	}
	if u.User != nil {
		return u.Redacted(), fmt.Errorf("the URL has user information")
	}
	if u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return text, fmt.Errorf("the URL is not an http or https URL")
	}
	return text, nil
}

// checkAIContradictions stops the pairs of keys that contradict each other. The
// check uses the values that apply, with the defaults.
func checkAIContradictions(cfg AIConfig) error {
	relay := cfg.Relay
	for _, pair := range []struct {
		lowerKey, upperKey string
		lower, upper       int
	}{
		{"relay.max_streams_per_run", "relay.max_streams", relay.MaxStreamsPerRun, relay.MaxStreams},
		{"relay.response_header_timeout_sec", "relay.total_timeout_sec", relay.ResponseHeaderTimeoutSec, relay.TotalTimeoutSec},
		{"relay.idle_timeout_sec", "relay.total_timeout_sec", relay.IdleTimeoutSec, relay.TotalTimeoutSec},
	} {
		if pair.lower > pair.upper {
			lowerKey, upperKey := AIBlock+"."+pair.lowerKey, AIBlock+"."+pair.upperKey
			return fmt.Errorf("config: keys %q and %q contradict each other: %q is %d and must not be above %q, which is %d",
				lowerKey, upperKey, lowerKey, pair.lower, upperKey, pair.upper)
		}
	}
	return nil
}

// loadAIConfig reads the block ai. The block has no default in the configuration
// reader and no environment variable: the file is the only source.
func (l *ConfigLoader) loadAIConfig(cfg *Config) error {
	parsed, err := ParseAIConfig(l.v.Get(AIBlock))
	if err != nil {
		return err
	}
	cfg.AI = parsed
	return nil
}
