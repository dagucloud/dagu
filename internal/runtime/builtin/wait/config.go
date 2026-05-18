// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package wait

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/go-viper/mapstructure/v2"
	"github.com/google/jsonschema-go/jsonschema"
)

type config struct {
	Duration       string            `mapstructure:"duration"`
	Until          string            `mapstructure:"until"`
	Path           string            `mapstructure:"path"`
	State          string            `mapstructure:"state"`
	URL            string            `mapstructure:"url"`
	Method         string            `mapstructure:"method"`
	Status         int               `mapstructure:"status"`
	Headers        map[string]string `mapstructure:"headers"`
	Body           string            `mapstructure:"body"`
	PollInterval   string            `mapstructure:"poll_interval"`
	RequestTimeout string            `mapstructure:"request_timeout"`
}

type parsedConfig struct {
	config
	duration       time.Duration
	until          time.Time
	pollInterval   time.Duration
	requestTimeout time.Duration
}

func defaultConfig() config {
	return config{
		State:          stateExists,
		Method:         "GET",
		Status:         200,
		PollInterval:   "1s",
		RequestTimeout: "10s",
	}
}

func decodeConfig(raw map[string]any, cfg *config) error {
	if raw == nil {
		raw = map[string]any{}
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

func parseConfig(operation string, cfg config) (parsedConfig, error) {
	parsed := parsedConfig{config: cfg}

	pollInterval, err := parsePositiveDuration("poll_interval", cfg.PollInterval)
	if err != nil {
		return parsed, err
	}
	parsed.pollInterval = pollInterval

	requestTimeout, err := parsePositiveDuration("request_timeout", cfg.RequestTimeout)
	if err != nil {
		return parsed, err
	}
	parsed.requestTimeout = requestTimeout

	switch operation {
	case opDuration:
		if strings.TrimSpace(cfg.Duration) == "" {
			return parsed, fmt.Errorf("wait: duration is required for %s", operation)
		}
		duration, err := parsePositiveDuration("duration", cfg.Duration)
		if err != nil {
			return parsed, err
		}
		parsed.duration = duration
	case opUntil:
		if strings.TrimSpace(cfg.Until) == "" {
			return parsed, fmt.Errorf("wait: until is required for %s", operation)
		}
		until, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(cfg.Until))
		if err != nil {
			return parsed, fmt.Errorf("wait: until must be RFC3339 timestamp: %w", err)
		}
		parsed.until = until
	case opFile:
		if strings.TrimSpace(cfg.Path) == "" {
			return parsed, fmt.Errorf("wait: path is required for %s", operation)
		}
		switch cfg.State {
		case stateExists, stateMissing:
		default:
			return parsed, fmt.Errorf("wait: state must be %q or %q", stateExists, stateMissing)
		}
	case opHTTP:
		if strings.TrimSpace(cfg.URL) == "" {
			return parsed, fmt.Errorf("wait: url is required for %s", operation)
		}
		parsedURL, err := url.ParseRequestURI(strings.TrimSpace(cfg.URL))
		if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
			return parsed, fmt.Errorf("wait: url must be an absolute HTTP URL")
		}
		if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
			return parsed, fmt.Errorf("wait: url must be an absolute HTTP URL")
		}
		if strings.TrimSpace(cfg.Method) == "" {
			return parsed, fmt.Errorf("wait: method must not be empty")
		}
		if cfg.Status < 100 || cfg.Status > 599 {
			return parsed, fmt.Errorf("wait: status must be between 100 and 599")
		}
	default:
		return parsed, fmt.Errorf("wait: unsupported operation %q", operation)
	}

	return parsed, nil
}

func parsePositiveDuration(field, value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("wait: %s must not be empty", field)
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("wait: %s must be a duration: %w", field, err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("wait: %s must be greater than 0", field)
	}
	return duration, nil
}

var configSchema = &jsonschema.Schema{
	Type:                 "object",
	AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	Properties: map[string]*jsonschema.Schema{
		"duration":        {Type: "string", Description: "Duration to wait for action: wait.duration, such as 30s or 5m."},
		"until":           {Type: "string", Format: "date-time", Description: "RFC3339 timestamp for action: wait.until."},
		"path":            {Type: "string", Description: "File or directory path for action: wait.file."},
		"state":           {Type: "string", Enum: []any{stateExists, stateMissing}, Description: "File state to wait for. Defaults to exists."},
		"url":             {Type: "string", Format: "uri", Description: "Absolute URL for action: wait.http."},
		"method":          {Type: "string", Description: "HTTP method for action: wait.http. Defaults to GET."},
		"status":          {Type: "integer", Minimum: new(float64(100)), Maximum: new(float64(599)), Description: "Expected HTTP status for action: wait.http. Defaults to 200."},
		"headers":         {Type: "object", AdditionalProperties: &jsonschema.Schema{Type: "string"}, Description: "HTTP headers for action: wait.http."},
		"body":            {Type: "string", Description: "HTTP request body for action: wait.http."},
		"poll_interval":   {Type: "string", Description: "Polling interval for wait.file and wait.http. Defaults to 1s."},
		"request_timeout": {Type: "string", Description: "Per-request timeout for wait.http. Defaults to 10s."},
	},
}

func init() {
	core.RegisterExecutorConfigSchema(executorType, configSchema)
}
