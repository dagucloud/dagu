// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package clickhouse provides the ClickHouse executor, which runs SQL against
// the ClickHouse HTTP interface.
package clickhouse

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/go-viper/mapstructure/v2"
)

// Output formats accepted by output_format.
const (
	formatJSONL = "jsonl"
	formatJSON  = "json"
	formatCSV   = "csv"
)

// Config represents the ClickHouse executor configuration.
type Config struct {
	// URL is the HTTP(S) endpoint of the ClickHouse server, for example
	// "http://localhost:8123" or "https://host:8443". Credentials are never
	// read from it.
	URL string `mapstructure:"url"`

	// User and Password are sent as the X-ClickHouse-User and X-ClickHouse-Key
	// headers.
	User     string `mapstructure:"user"`
	Password string `mapstructure:"password"`

	// Database is the default database of every statement.
	Database string `mapstructure:"database"`

	// File is the path of a .sql file; it replaces the inline query, which
	// the clickhouse.query action moves from with.query to the step command.
	File string `mapstructure:"file"`

	// Params are server-side query parameters. Each entry is bound to the
	// {name:Type} placeholder of the same name.
	Params map[string]any `mapstructure:"params"`

	// Settings are passed to the server as URL settings, for example
	// readonly or max_execution_time.
	Settings map[string]any `mapstructure:"settings"`

	// Output settings.
	OutputFormat string `mapstructure:"output_format"` // jsonl (default), json, csv
	Headers      bool   `mapstructure:"headers"`       // Include a header row in CSV output

	// Execution settings.
	Timeout       int    `mapstructure:"timeout"`         // Step timeout in seconds (default: 60, 0 = none)
	TLSSkipVerify bool   `mapstructure:"tls_skip_verify"` // Skip TLS certificate verification
	TLSCA         string `mapstructure:"tls_ca"`          // PEM bundle of additional root CAs
}

// DefaultConfig returns a Config with default values.
func DefaultConfig() *Config {
	return &Config{
		Timeout:      60,
		OutputFormat: formatJSONL,
	}
}

var nameRegexp = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// reservedSettings are URL parameters the executor sets itself; a settings
// entry may not shadow them.
var reservedSettings = map[string]string{
	"query":           "use query or file",
	"database":        "use database",
	"user":            "use user",
	"password":        "use password",
	"default_format":  "use output_format",
	"query_id":        "the executor assigns one to every statement",
	"session_id":      "the executor manages the session",
	"session_check":   "the executor manages the session",
	"session_timeout": "the executor manages the session",
	"compress":        "the response is read uncompressed",
	"decompress":      "the request is sent uncompressed",
	// An error written into the data would end the body normally, and the
	// step would not fail.
	"http_write_exception_in_output_format": "the executor needs failures reported out of band",
}

// ParseConfig parses the executor configuration from a map and validates it.
func ParseConfig(mapCfg map[string]any) (*Config, error) {
	cfg := DefaultConfig()

	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:           cfg,
		WeaklyTypedInput: true,
		ErrorUnused:      true, // a misspelled option must not be ignored
		TagName:          "mapstructure",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create config decoder: %w", err)
	}
	if err := decoder.Decode(mapCfg); err != nil {
		return nil, fmt.Errorf("failed to decode clickhouse config: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if strings.TrimSpace(c.URL) == "" {
		return fmt.Errorf("url is required")
	}
	if err := validateURL(c.URL); err != nil {
		return err
	}

	switch c.OutputFormat {
	case formatJSONL, formatJSON, formatCSV:
	default:
		return fmt.Errorf("invalid output_format: %s (must be jsonl, json, or csv)", c.OutputFormat)
	}
	if c.Timeout < 0 {
		return fmt.Errorf("timeout must be non-negative")
	}

	for name := range c.Params {
		if !nameRegexp.MatchString(name) {
			return fmt.Errorf("invalid params name %q (must be an identifier)", name)
		}
	}
	for name, value := range c.Params {
		if _, err := formatParam(value); err != nil {
			return fmt.Errorf("params %q: %w", name, err)
		}
	}
	for name, value := range c.Settings {
		if !nameRegexp.MatchString(name) {
			return fmt.Errorf("invalid settings name %q (must be an identifier)", name)
		}
		if hint, reserved := reservedSettings[name]; reserved {
			return fmt.Errorf("settings %q is not allowed: %s", name, hint)
		}
		if strings.HasPrefix(name, paramPrefix) {
			return fmt.Errorf("settings %q is not allowed: use params", name)
		}
		if _, err := formatSetting(value); err != nil {
			return fmt.Errorf("settings %q: %w", name, err)
		}
	}
	return nil
}

// validateURL checks that raw is an http(s) endpoint without embedded
// credentials, query string, or fragment.
func validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid url: %w", redactURLError(err))
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("invalid url: scheme must be http or https")
	}
	if u.Host == "" {
		return fmt.Errorf("invalid url: host is required")
	}
	if u.User != nil {
		return fmt.Errorf("invalid url: credentials must not be embedded in the url; use user and password")
	}
	if u.RawQuery != "" || u.ForceQuery {
		return fmt.Errorf("invalid url: query string is not allowed; use database, settings, and params")
	}
	if u.Fragment != "" {
		return fmt.Errorf("invalid url: fragment is not allowed")
	}
	return nil
}

// redactURLError strips the input URL, which may hold credentials, from a
// url.Parse error.
func redactURLError(err error) error {
	if ue, ok := err.(*url.Error); ok {
		return ue.Err
	}
	return err
}
