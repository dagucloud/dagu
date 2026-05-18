// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sql

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/go-viper/mapstructure/v2"
	"github.com/google/jsonschema-go/jsonschema"
)

// Config represents the SQL executor configuration.
type Config struct {
	// DSN is the data source name for database connection.
	// Format depends on the driver:
	// - PostgreSQL: "postgres://user:pass@host:port/dbname?sslmode=disable"
	// - SQLite: "file:./data.db?mode=rw" or ":memory:"
	// - DuckDB: "./data.duckdb" or ":memory:"
	DSN string `mapstructure:"dsn"`

	// Parameterized queries (SQL injection prevention)
	// Can be map[string]any for named params or []any for positional params
	Params any `mapstructure:"params"`

	// Execution settings
	Timeout        int    `mapstructure:"timeout"`         // Query timeout in seconds (default: 60)
	Transaction    bool   `mapstructure:"transaction"`     // Wrap execution in transaction
	IsolationLevel string `mapstructure:"isolation_level"` // Transaction isolation level

	// Locking
	AdvisoryLock string `mapstructure:"advisory_lock"` // Named advisory lock (PostgreSQL)
	FileLock     bool   `mapstructure:"file_lock"`     // Use file locking (SQLite)

	// SQLite-specific options
	SharedMemory bool `mapstructure:"shared_memory"` // Enable shared cache for :memory: databases (SQLite)

	// Output settings
	OutputFormat string `mapstructure:"output_format"` // jsonl (default), json, csv
	Headers      bool   `mapstructure:"headers"`       // Include headers in CSV output
	NullString   string `mapstructure:"null_string"`   // String representation for NULL values

	// Large result handling
	MaxRows    int    `mapstructure:"max_rows"`    // Maximum rows to return (0 = unlimited)
	Streaming  bool   `mapstructure:"streaming"`   // Stream results to file
	OutputFile string `mapstructure:"output_file"` // File path for streaming output

	// Import settings
	Import *ImportConfig `mapstructure:"import"` // Import data from file
}

// ImportConfig configures data import from CSV/TSV/JSONL files.
type ImportConfig struct {
	// Required fields
	InputFile string `mapstructure:"input_file"` // Path to input file
	Table     string `mapstructure:"table"`      // Target table name

	// Format options
	Format    string `mapstructure:"format"`     // csv, tsv, jsonl (auto-detect if empty)
	HasHeader *bool  `mapstructure:"has_header"` // Whether first row is header (default: true for csv/tsv)
	Delimiter string `mapstructure:"delimiter"`  // Field delimiter (default: "," for csv, "\t" for tsv)

	// Column mapping
	Columns []string `mapstructure:"columns"` // Explicit column names (overrides header)

	// NULL handling
	NullValues []string `mapstructure:"null_values"` // Values to treat as NULL

	// Batch settings
	BatchSize int `mapstructure:"batch_size"` // Rows per INSERT statement (default: 1000)

	// Conflict handling
	OnConflict     string   `mapstructure:"on_conflict"`     // error (default), ignore, replace
	ConflictTarget string   `mapstructure:"conflict_target"` // Column(s) for conflict detection (required for PostgreSQL UPSERT with "replace")
	UpdateColumns  []string `mapstructure:"update_columns"`  // Columns to update on conflict (driver support varies)

	// Row limits
	SkipRows int `mapstructure:"skip_rows"` // Skip first N data rows
	MaxRows  int `mapstructure:"max_rows"`  // Limit import (0 = unlimited)

	// Validation
	DryRun bool `mapstructure:"dry_run"` // Validate without importing
}

// DefaultConfig returns a Config with default values.
func DefaultConfig() *Config {
	return &Config{
		Timeout:      60,
		OutputFormat: "jsonl",
		NullString:   "null",
	}
}

// ParseConfig parses the executor configuration from a map.
func ParseConfig(_ context.Context, mapCfg map[string]any) (*Config, error) {
	cfg := DefaultConfig()

	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:           cfg,
		WeaklyTypedInput: true,
		TagName:          "mapstructure",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create config decoder: %w", err)
	}

	if err := decoder.Decode(mapCfg); err != nil {
		return nil, fmt.Errorf("failed to decode sql config: %w", err)
	}

	// Validate required fields
	if cfg.DSN == "" {
		return nil, fmt.Errorf("dsn is required")
	}

	// Validate output format
	switch cfg.OutputFormat {
	case "jsonl", "json", "csv":
		// Valid
	default:
		return nil, fmt.Errorf("invalid output_format: %s (must be jsonl, json, or csv)", cfg.OutputFormat)
	}

	// Validate isolation level if specified
	if cfg.IsolationLevel != "" {
		switch cfg.IsolationLevel {
		case "default", "read_committed", "repeatable_read", "serializable":
			// Valid - "default" uses database's default isolation level
		default:
			return nil, fmt.Errorf("invalid isolation_level: %s (valid: default, read_committed, repeatable_read, serializable)", cfg.IsolationLevel)
		}
	}

	if cfg.Timeout < 0 {
		return nil, fmt.Errorf("timeout must be non-negative")
	}

	// Validate and set defaults for import config
	if cfg.Import != nil {
		if err := cfg.Import.validate(); err != nil {
			return nil, fmt.Errorf("invalid import config: %w", err)
		}
		cfg.Import.setDefaults()
	}

	return cfg, nil
}

// validate checks required fields and valid values for ImportConfig.
func (c *ImportConfig) validate() error {
	if c.InputFile == "" {
		return fmt.Errorf("input_file is required")
	}
	if c.Table == "" {
		return fmt.Errorf("table is required")
	}

	// Validate format if specified
	if c.Format != "" {
		switch c.Format {
		case "csv", "tsv", "jsonl":
			// Valid
		default:
			return fmt.Errorf("invalid format: %s (must be csv, tsv, or jsonl)", c.Format)
		}
	}

	// Validate on_conflict if specified
	if c.OnConflict != "" {
		switch c.OnConflict {
		case "error", "ignore", "replace":
			// Valid
		default:
			return fmt.Errorf("invalid on_conflict: %s (must be error, ignore, or replace)", c.OnConflict)
		}
	}

	// Validate batch size
	if c.BatchSize < 0 {
		return fmt.Errorf("batch_size must be non-negative")
	}

	// Validate row limits
	if c.SkipRows < 0 {
		return fmt.Errorf("skip_rows must be non-negative")
	}
	if c.MaxRows < 0 {
		return fmt.Errorf("max_rows must be non-negative")
	}

	return nil
}

// setDefaults applies default values to ImportConfig.
func (c *ImportConfig) setDefaults() {
	if c.Format == "" {
		c.Format = DetectFormat(c.InputFile)
	}
	if c.BatchSize == 0 {
		c.BatchSize = 1000
	}
	if c.OnConflict == "" {
		c.OnConflict = "error"
	}
	if len(c.NullValues) == 0 {
		c.NullValues = []string{"", "NULL", "null", "\\N"}
	}
	// HasHeader defaults to true for CSV/TSV (most files have headers)
	if c.HasHeader == nil {
		c.HasHeader = new(true)
	}
}

// GetNamedParams returns params as a map if they are named parameters.
func (c *Config) GetNamedParams() (map[string]any, bool) {
	if c.Params == nil {
		return nil, false
	}
	params, ok := c.Params.(map[string]any)
	return params, ok
}

// GetPositionalParams returns params as a slice if they are positional parameters.
func (c *Config) GetPositionalParams() ([]any, bool) {
	if c.Params == nil {
		return nil, false
	}
	params, ok := c.Params.([]any)
	return params, ok
}

// importConfigSchema defines the JSON schema for import configuration.
var importConfigSchema = &jsonschema.Schema{
	Type:        "object",
	Description: "Import data from CSV/TSV/JSONL file",
	Properties: map[string]*jsonschema.Schema{
		"input_file":      {Type: "string", Description: "Path to input file"},
		"table":           {Type: "string", Description: "Target table name"},
		"format":          {Type: "string", Enum: []any{"csv", "tsv", "jsonl"}, Description: "Input format (auto-detected from file extension if not specified)"},
		"has_header":      {Type: "boolean", Description: "Whether first row is header (default: true)"},
		"delimiter":       {Type: "string", Description: "Field delimiter (default: ',' for csv, '\\t' for tsv)"},
		"columns":         {Type: "array", Items: &jsonschema.Schema{Type: "string"}, Description: "Explicit column names"},
		"null_values":     {Type: "array", Items: &jsonschema.Schema{Type: "string"}, Description: "Values to treat as NULL"},
		"batch_size":      {Type: "integer", Description: "Rows per INSERT statement (default: 1000)"},
		"on_conflict":     {Type: "string", Enum: []any{"error", "ignore", "replace"}, Description: "Conflict handling (default: error)"},
		"conflict_target": {Type: "string", Description: "Column(s) for conflict detection (required for PostgreSQL UPSERT with 'replace')"},
		"update_columns":  {Type: "array", Items: &jsonschema.Schema{Type: "string"}, Description: "Columns to update on conflict (if empty, updates all non-key columns)"},
		"skip_rows":       {Type: "integer", Description: "Skip first N data rows"},
		"max_rows":        {Type: "integer", Description: "Limit import rows (0 = unlimited)"},
		"dry_run":         {Type: "boolean", Description: "Validate without importing"},
	},
	Required: []string{"input_file", "table"},
}

// JSON Schema for SQL executor configurations
var postgresConfigSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"dsn": {Type: "string", Description: "PostgreSQL connection string (DSN)"},
		"params": {
			Description: "Query parameters (map for named, array for positional)",
			OneOf: []*jsonschema.Schema{
				{Type: "object", AdditionalProperties: &jsonschema.Schema{}},
				{Type: "array"},
			},
		},
		"timeout":         {Type: "integer", Description: "Query timeout in seconds"},
		"transaction":     {Type: "boolean", Description: "Wrap execution in transaction"},
		"isolation_level": {Type: "string", Enum: []any{"default", "read_committed", "repeatable_read", "serializable"}, Description: "Transaction isolation level"},
		"advisory_lock":   {Type: "string", Description: "Named advisory lock"},
		"output_format":   {Type: "string", Enum: []any{"jsonl", "json", "csv"}, Description: "Output format"},
		"headers":         {Type: "boolean", Description: "Include headers in CSV output"},
		"null_string":     {Type: "string", Description: "String representation for NULL values"},
		"max_rows":        {Type: "integer", Description: "Maximum rows to return"},
		"streaming":       {Type: "boolean", Description: "Stream results to file"},
		"output_file":     {Type: "string", Description: "File path for streaming output"},
		"import":          importConfigSchema,
	},
	Required: []string{"dsn"},
}

var sqliteConfigSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"dsn": {Type: "string", Description: "SQLite connection string (file path or :memory:)"},
		"params": {
			Description: "Query parameters (map for named, array for positional)",
			OneOf: []*jsonschema.Schema{
				{Type: "object", AdditionalProperties: &jsonschema.Schema{}},
				{Type: "array"},
			},
		},
		"timeout":       {Type: "integer", Description: "Query timeout in seconds"},
		"transaction":   {Type: "boolean", Description: "Wrap execution in transaction"},
		"file_lock":     {Type: "boolean", Description: "Use file locking for exclusive access"},
		"shared_memory": {Type: "boolean", Description: "Enable shared cache for :memory: databases to share data across DAG steps"},
		"output_format": {Type: "string", Enum: []any{"jsonl", "json", "csv"}, Description: "Output format"},
		"headers":       {Type: "boolean", Description: "Include headers in CSV output"},
		"null_string":   {Type: "string", Description: "String representation for NULL values"},
		"max_rows":      {Type: "integer", Description: "Maximum rows to return"},
		"streaming":     {Type: "boolean", Description: "Stream results to file"},
		"output_file":   {Type: "string", Description: "File path for streaming output"},
		"import":        importConfigSchema,
	},
	Required: []string{"dsn"},
}

var duckdbConfigSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"dsn": {Type: "string", Description: "DuckDB database path or :memory:"},
		"params": {
			Description: "Query parameters (map for named, array for positional)",
			OneOf: []*jsonschema.Schema{
				{Type: "object", AdditionalProperties: &jsonschema.Schema{}},
				{Type: "array"},
			},
		},
		"timeout":       {Type: "integer", Description: "Query timeout in seconds"},
		"transaction":   {Type: "boolean", Description: "Wrap execution in transaction"},
		"output_format": {Type: "string", Enum: []any{"jsonl", "json", "csv"}, Description: "Output format"},
		"headers":       {Type: "boolean", Description: "Include headers in CSV output"},
		"null_string":   {Type: "string", Description: "String representation for NULL values"},
		"max_rows":      {Type: "integer", Description: "Maximum rows to return"},
		"streaming":     {Type: "boolean", Description: "Stream results to file"},
		"output_file":   {Type: "string", Description: "File path for streaming output"},
		"import":        importConfigSchema,
	},
	Required: []string{"dsn"},
}

func init() {
	core.RegisterExecutorConfigSchema("duckdb", duckdbConfigSchema)
	core.RegisterExecutorConfigSchema("postgres", postgresConfigSchema)
	core.RegisterExecutorConfigSchema("sqlite", sqliteConfigSchema)
}
