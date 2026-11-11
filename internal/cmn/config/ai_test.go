// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// aiYAML writes the key at the dotted path as a block of the file ai.
func aiYAML(path string, value string) string {
	parts := strings.Split(path, ".")
	var b strings.Builder
	b.WriteString("ai:\n")
	for i, part := range parts {
		b.WriteString(strings.Repeat("  ", i+1) + part + ":")
		if i == len(parts)-1 {
			b.WriteString(" " + value + "\n")
		} else {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func loadAIError(t *testing.T, yaml string) error {
	t.Helper()
	file := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(file, []byte(yaml), 0o600))
	_, err := NewConfigLoader(viper.New(), WithAppHomeDir(t.TempDir()), WithConfigFile(file)).Load()
	return err
}

// The block ai has the defaults of Spec 093, each value says that it is a
// default, and an environment variable does not set a key of the block.
func TestAIDefaults(t *testing.T) {
	t.Setenv("DAGU_AI_MODELS_REFRESH_HOURS", "3")
	t.Setenv("DAGU_AI_RELAY_MAX_STREAMS", "3")
	t.Setenv("DAGU_AI_UPSTREAM_PROXY_URL", "http://proxy.example.com")
	cfg := testLoad(t)
	ai := cfg.AI

	assert.Equal(t, 24, ai.ModelsRefreshHours)
	assert.Equal(t, 30, ai.ModelsTimeoutSec)
	assert.Empty(t, ai.Upstream.ProxyURL)
	assert.Equal(t, AIRelayConfig{
		MaxRequestBytes:          16777216,
		MaxResponseBytes:         268435456,
		MaxHeaderBytes:           32768,
		MaxStreamsPerRun:         8,
		MaxStreams:               128,
		ConnectTimeoutSec:        10,
		ResponseHeaderTimeoutSec: 600,
		IdleTimeoutSec:           300,
		TotalTimeoutSec:          3600,
	}, ai.Relay)
	require.Len(t, ai.Sources, len(AIKeyDocs()))
	for _, doc := range AIKeyDocs() {
		assert.Equal(t, "default", ai.Sources[doc.Key], doc.Key)
	}
}

// The file sets each key, and the source of each value is the file.
func TestAIFromFile(t *testing.T) {
	cfg := loadFromYAML(t, `
ai:
  models_refresh_hours: 0
  models_timeout_sec: 5
  upstream:
    proxy_url: "https://proxy.example.com:3128"
  relay:
    max_request_bytes: 2048
    max_response_bytes: 4096
    max_header_bytes: 2048
    max_streams_per_run: 2
    max_streams: 4
    connect_timeout_sec: 3
    response_header_timeout_sec: 20
    idle_timeout_sec: 30
    total_timeout_sec: 60
`)
	ai := cfg.AI
	assert.Equal(t, 0, ai.ModelsRefreshHours, "zero stops the automatic refresh")
	assert.Equal(t, 5, ai.ModelsTimeoutSec)
	assert.Equal(t, "https://proxy.example.com:3128", ai.Upstream.ProxyURL)
	assert.Equal(t, AIRelayConfig{
		MaxRequestBytes: 2048, MaxResponseBytes: 4096, MaxHeaderBytes: 2048,
		MaxStreamsPerRun: 2, MaxStreams: 4, ConnectTimeoutSec: 3,
		ResponseHeaderTimeoutSec: 20, IdleTimeoutSec: 30, TotalTimeoutSec: 60,
	}, ai.Relay)
	for _, doc := range AIKeyDocs() {
		assert.Equal(t, "file", ai.Sources[doc.Key], doc.Key)
	}
}

// A key that the file leaves out keeps the default and its source default.
func TestAIPartialFileKeepsTheOtherDefaults(t *testing.T) {
	cfg := loadFromYAML(t, "ai:\n  relay:\n    idle_timeout_sec: 120\n")
	assert.Equal(t, 120, cfg.AI.Relay.IdleTimeoutSec)
	assert.Equal(t, "file", cfg.AI.Sources["relay.idle_timeout_sec"])
	assert.Equal(t, 3600, cfg.AI.Relay.TotalTimeoutSec)
	assert.Equal(t, "default", cfg.AI.Sources["relay.total_timeout_sec"])
}

// Each integer key accepts its two limits and refuses the value outside them. The
// error names the key, the range and the value, and no value is changed.
func TestAIRangeOfEachKey(t *testing.T) {
	for _, key := range aiKeys {
		if !key.integer {
			continue
		}
		t.Run(key.path, func(t *testing.T) {
			for _, ok := range []int{key.min, key.max} {
				cfg := newAIConfig()
				require.NoError(t, setAIKey(&cfg, key.path, ok), "value %d", ok)
				assert.Equal(t, ok, *key.field(&cfg))
			}
			for _, bad := range []int{key.min - 1, key.max + 1} {
				err := loadAIError(t, aiYAML(key.path, strconv.Itoa(bad)))
				require.Error(t, err, "value %d", bad)
				assert.Contains(t, err.Error(), fmt.Sprintf("key %q", "ai."+key.path))
				assert.Contains(t, err.Error(), fmt.Sprintf("expected an integer from %d to %d", key.min, key.max))
				assert.Contains(t, err.Error(), fmt.Sprintf("got %d", bad))
			}
		})
	}
}

// The example of the spec.
func TestAIErrorMessageOfTheSpec(t *testing.T) {
	err := loadAIError(t, aiYAML("relay.max_streams_per_run", "0"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `config: key "ai.relay.max_streams_per_run": expected an integer from 1 to 256, got 0 (int)`)
}

// The block is closed and strict: an unknown key at each level, a duplicate key, a
// value of the wrong type and a block of the wrong shape stop Dagu at start.
func TestAIErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		yaml string
		want []string
	}{
		{"unknown key", "ai:\n  model_timeout_sec: 3\n", []string{`unknown key "ai.model_timeout_sec"`, "valid keys are models_refresh_hours, models_timeout_sec, relay, upstream"}},
		{"unknown relay key", "ai:\n  relay:\n    max_stream: 3\n", []string{`unknown key "ai.relay.max_stream"`, "in the block ai.relay", "valid keys are connect_timeout_sec, idle_timeout_sec, max_header_bytes"}},
		{"unknown upstream key", "ai:\n  upstream:\n    proxy: x\n", []string{`unknown key "ai.upstream.proxy"`, "valid keys are proxy_url"}},
		{"api key is not a key", "ai:\n  api_key: sk-123\n", []string{`unknown key "ai.api_key"`}},
		{"env key is not a key", "ai:\n  relay:\n    max_streams_env: X\n", []string{`unknown key "ai.relay.max_streams_env"`}},
		{"string for integer", "ai:\n  models_timeout_sec: \"30\"\n", []string{`key "ai.models_timeout_sec"`, "an integer from 1 to 300", `got "30"`}},
		{"fraction", "ai:\n  models_timeout_sec: 2.5\n", []string{`key "ai.models_timeout_sec"`, "2.5"}},
		{"integral float", "ai:\n  models_timeout_sec: 30.0\n", []string{`key "ai.models_timeout_sec"`, "an integer from 1 to 300", "float64"}},
		{"bool for integer", "ai:\n  relay:\n    max_streams: true\n", []string{`key "ai.relay.max_streams"`, "got true"}},
		{"list for integer", "ai:\n  relay:\n    max_streams: [1]\n", []string{`key "ai.relay.max_streams"`, "a list with 1 items"}},
		{"integer for proxy", "ai:\n  upstream:\n    proxy_url: 3\n", []string{`key "ai.upstream.proxy_url"`, "an http or https URL"}},
		{"block is not an object", "ai: yes\n", []string{`key "ai"`, "expected an object"}},
		{"relay is not an object", "ai:\n  relay: 3\n", []string{`key "ai.relay"`, "expected an object"}},
		{"upstream is a string", "ai:\n  upstream: x\n", []string{`key "ai.upstream"`, "expected an object"}},
		{"integer key as object", "ai:\n  models_timeout_sec:\n    a: 1\n", []string{`key "ai.models_timeout_sec"`, "an integer from 1 to 300", "an object"}},
		{"duplicate key", "ai:\n  relay:\n    max_streams: 2\n    max_streams: 3\n", []string{"max_streams"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := loadAIError(t, tc.yaml)
			require.Error(t, err)
			for _, part := range tc.want {
				assert.Contains(t, err.Error(), part)
			}
		})
	}
}

// The proxy URL is an http or https URL with no user information. An empty value
// means a direct call. The error never shows the user information.
func TestAIProxyURL(t *testing.T) {
	for _, ok := range []string{"http://proxy.example.com", "https://proxy.example.com:3128", "http://10.0.0.1:8080/", `""`} {
		cfg := loadFromYAML(t, aiYAML("upstream.proxy_url", ok))
		assert.Equal(t, strings.Trim(ok, `"`), cfg.AI.Upstream.ProxyURL)
	}
	for _, bad := range []string{"ftp://proxy.example.com", "proxy.example.com:3128", "http://", "socks5://proxy.example.com"} {
		err := loadAIError(t, aiYAML("upstream.proxy_url", `"`+bad+`"`))
		require.Error(t, err, bad)
		assert.Contains(t, err.Error(), `key "ai.upstream.proxy_url"`, bad)
		assert.Contains(t, err.Error(), bad, "the error shows the value")
	}
	err := loadAIError(t, aiYAML("upstream.proxy_url", `"http://user:hunter2@proxy.example.com"`))
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "hunter2")
	assert.Contains(t, err.Error(), "user:xxxxx@proxy.example.com")
}

// Two keys that contradict each other stop Dagu at start. The check uses the
// values that apply: a default can be the other side of the pair.
func TestAIContradictions(t *testing.T) {
	for _, tc := range []struct {
		name string
		yaml string
		want []string
	}{
		{"streams per run above streams", "ai:\n  relay:\n    max_streams_per_run: 10\n    max_streams: 9\n",
			[]string{"contradict each other", `"ai.relay.max_streams_per_run" is 10`, `"ai.relay.max_streams", which is 9`}},
		{"streams below the default of the run", "ai:\n  relay:\n    max_streams: 4\n",
			[]string{`"ai.relay.max_streams_per_run" is 8`, `"ai.relay.max_streams", which is 4`}},
		{"response header above total", "ai:\n  relay:\n    response_header_timeout_sec: 100\n    total_timeout_sec: 60\n    idle_timeout_sec: 10\n",
			[]string{`"ai.relay.response_header_timeout_sec" is 100`, `"ai.relay.total_timeout_sec", which is 60`}},
		{"idle above total", "ai:\n  relay:\n    idle_timeout_sec: 100\n    total_timeout_sec: 60\n    response_header_timeout_sec: 10\n",
			[]string{`"ai.relay.idle_timeout_sec" is 100`, `"ai.relay.total_timeout_sec", which is 60`}},
		{"total below the default of the header", "ai:\n  relay:\n    total_timeout_sec: 500\n",
			[]string{`"ai.relay.response_header_timeout_sec" is 600`, `"ai.relay.total_timeout_sec", which is 500`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := loadAIError(t, tc.yaml)
			require.Error(t, err)
			for _, part := range tc.want {
				assert.Contains(t, err.Error(), part)
			}
		})
	}

	// Equal values do not contradict.
	cfg := loadFromYAML(t, "ai:\n  relay:\n    max_streams_per_run: 8\n    max_streams: 8\n    response_header_timeout_sec: 60\n    idle_timeout_sec: 60\n    total_timeout_sec: 60\n")
	assert.Equal(t, 8, cfg.AI.Relay.MaxStreams)
}

// The table of keys holds each key once, with a default that the parser accepts.
func TestAIKeyDocs(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}
	for _, doc := range AIKeyDocs() {
		assert.False(t, seen[doc.Key], doc.Key)
		seen[doc.Key] = true
		assert.NotEmpty(t, doc.Default, doc.Key)
		assert.NotEmpty(t, doc.Description, doc.Key)
		assert.NotEmpty(t, doc.Range, doc.Key)
	}
	assert.Len(t, seen, len(aiKeys))
	parsed, err := ParseAIConfig(nil)
	require.NoError(t, err)
	assert.Len(t, parsed.Sources, len(aiKeys))
	// The defaults are inside their ranges, and they do not contradict each other.
	require.NoError(t, checkAIContradictions(parsed))
	for _, key := range aiKeys {
		if key.integer {
			assert.GreaterOrEqual(t, key.def, key.min, key.path)
			assert.LessOrEqual(t, key.def, key.max, key.path)
		}
	}
	values := parsed.Values()
	assert.Equal(t, "(empty)", values["upstream.proxy_url"])
	assert.Equal(t, "128", values["relay.max_streams"])
}

// The table of Spec 093, "Configuration", and the table of the code agree: the keys, the order, the
// defaults and the ranges. The reference of the keys is the table of the code, so
// a change of one needs a change of the other.
func TestAIKeyDocsMatchTheSpec(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("..", "..", "..", "specs", "093-ai-endpoints.md"))
	require.NoError(t, err)
	row := regexp.MustCompile("^\\| `ai\\.([a-z_.]+)` \\| (integer|string) \\| ([^|]+?) \\| ([^|]+?) \\|$")
	rangePattern := regexp.MustCompile(`^(\d+) to (\d+)`)

	var specKeys []string
	for line := range strings.SplitSeq(string(data), "\n") {
		m := row.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		specKeys = append(specKeys, m[1])
		var doc *AIKeyDoc
		for _, d := range AIKeyDocs() {
			if d.Key == m[1] {
				doc = &d
			}
		}
		require.NotNil(t, doc, "the spec has the key %s, and the code has not", m[1])
		assert.Equal(t, doc.Type, m[2], m[1])
		assert.Equal(t, doc.Default, m[3], m[1])
		if doc.Type == "integer" {
			r := rangePattern.FindStringSubmatch(m[4])
			require.NotNil(t, r, m[1])
			assert.Equal(t, doc.Range, r[1]+" to "+r[2], m[1])
		}
	}
	var codeKeys []string
	for _, d := range AIKeyDocs() {
		codeKeys = append(codeKeys, d.Key)
	}
	assert.Equal(t, codeKeys, specKeys)
}
