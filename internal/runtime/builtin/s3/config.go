// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package s3

import (
	"fmt"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/go-viper/mapstructure/v2"
	"github.com/google/jsonschema-go/jsonschema"
)

// Config contains runtime options for the S3 executor.
type Config struct {
	// AWS Connection
	Region          string `mapstructure:"region"`
	Endpoint        string `mapstructure:"endpoint"`
	AccessKeyID     string `mapstructure:"access_key_id"`
	SecretAccessKey string `mapstructure:"secret_access_key"`
	SessionToken    string `mapstructure:"session_token"`
	Profile         string `mapstructure:"profile"`

	// Path style access (for S3-compatible services like MinIO)
	ForcePathStyle bool `mapstructure:"force_path_style"`

	// Common
	Bucket      string `mapstructure:"bucket"`
	Key         string `mapstructure:"key"`
	Source      string `mapstructure:"source"`
	Destination string `mapstructure:"destination"`

	// Upload options
	ContentType  string            `mapstructure:"content_type"`
	StorageClass string            `mapstructure:"storage_class"`
	Metadata     map[string]string `mapstructure:"metadata"`

	// Server-side encryption
	ServerSideEncryption string `mapstructure:"sse"`            // AES256, aws:kms
	SSEKMSKeyId          string `mapstructure:"sse_kms_key_id"` // KMS key ID for aws:kms

	// Access control
	ACL string `mapstructure:"acl"` // private, public-read, etc.

	// Object tagging
	Tags map[string]string `mapstructure:"tags"`

	// Transfer options
	PartSize    int64 `mapstructure:"part_size"`   // MB, default 10
	Concurrency int   `mapstructure:"concurrency"` // default 5

	// List options
	Prefix       string `mapstructure:"prefix"`
	Delimiter    string `mapstructure:"delimiter"`
	MaxKeys      int    `mapstructure:"max_keys"`
	Recursive    bool   `mapstructure:"recursive"`
	OutputFormat string `mapstructure:"output_format"` // json, jsonl

	// Delete options
	Quiet bool `mapstructure:"quiet"` // Suppress output for delete

	// Advanced options
	DisableSSL bool `mapstructure:"disable_ssl"` // For local testing only
}

var (
	// ValidStorageClasses is the list of valid S3 storage classes.
	ValidStorageClasses = []string{
		"STANDARD", "REDUCED_REDUNDANCY", "STANDARD_IA",
		"ONEZONE_IA", "INTELLIGENT_TIERING", "GLACIER",
		"DEEP_ARCHIVE", "GLACIER_IR",
	}

	// ValidACLs is the list of valid canned ACLs.
	ValidACLs = []string{
		"private", "public-read", "public-read-write",
		"authenticated-read", "aws-exec-read", "bucket-owner-read",
		"bucket-owner-full-control",
	}
)

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		PartSize:     10, // 10 MB
		Concurrency:  5,
		MaxKeys:      1000,
		OutputFormat: "json",
	}
}

// ApplyDefaults maps the core.S3Config values to the Config struct
// if they are present and not already set in the Config.
// Note: This sets DAG-level defaults BEFORE step config is decoded,
// so step config will override these values.
func (c *Config) ApplyDefaults(defaults *core.S3Config) {
	if defaults == nil {
		return
	}
	if defaults.Region != "" {
		c.Region = defaults.Region
	}
	if defaults.Endpoint != "" {
		c.Endpoint = defaults.Endpoint
	}
	if defaults.AccessKeyID != "" {
		c.AccessKeyID = defaults.AccessKeyID
	}
	if defaults.SecretAccessKey != "" {
		c.SecretAccessKey = defaults.SecretAccessKey
	}
	if defaults.SessionToken != "" {
		c.SessionToken = defaults.SessionToken
	}
	if defaults.Profile != "" {
		c.Profile = defaults.Profile
	}
	c.ForcePathStyle = defaults.ForcePathStyle
	c.DisableSSL = defaults.DisableSSL
	if defaults.Bucket != "" {
		c.Bucket = defaults.Bucket
	}
}

// decodeConfig decodes the raw config map into a Config struct.
func decodeConfig(raw map[string]any, cfg *Config) error {
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:           cfg,
		WeaklyTypedInput: true,
		ErrorUnused:      false,
		TagName:          "mapstructure",
	})
	if err != nil {
		return err
	}
	return decoder.Decode(raw)
}

// ValidateForOperation validates the config for the given operation.
func (c *Config) ValidateForOperation(operation string) error {
	if c == nil {
		return fmt.Errorf("%w: missing configuration", ErrConfig)
	}

	// Bucket is required for all operations
	if c.Bucket == "" {
		return fmt.Errorf("%w: bucket is required", ErrConfig)
	}

	switch operation {
	case opUpload:
		if c.Source == "" {
			return fmt.Errorf("%w: source is required for upload", ErrConfig)
		}
		if c.Key == "" {
			return fmt.Errorf("%w: key is required for upload", ErrConfig)
		}
	case opDownload:
		if c.Key == "" {
			return fmt.Errorf("%w: key is required for download", ErrConfig)
		}
		if c.Destination == "" {
			return fmt.Errorf("%w: destination is required for download", ErrConfig)
		}
	case opList:
		// Bucket is already validated above
	case opDelete:
		// Delete needs either key or prefix
		if c.Key == "" && c.Prefix == "" {
			return fmt.Errorf("%w: key or prefix is required for delete", ErrConfig)
		}
	default:
		return fmt.Errorf("%w: unknown operation %q", ErrConfig, operation)
	}

	// Validate and normalize storage class if provided
	if c.StorageClass != "" {
		if !containsIgnoreCase(ValidStorageClasses, c.StorageClass) {
			return fmt.Errorf("%w: invalid storage class %q", ErrConfig, c.StorageClass)
		}
		c.StorageClass = strings.ToUpper(c.StorageClass)
	}

	// Validate output format
	if c.OutputFormat != "" && c.OutputFormat != "json" && c.OutputFormat != "jsonl" {
		return fmt.Errorf("%w: output_format must be 'json' or 'jsonl'", ErrConfig)
	}

	// Validate server-side encryption
	if c.ServerSideEncryption != "" {
		if !containsIgnoreCase([]string{"AES256", "aws:kms"}, c.ServerSideEncryption) {
			return fmt.Errorf("%w: sse must be 'AES256' or 'aws:kms'", ErrConfig)
		}
		// KMS key ID is required for aws:kms
		if strings.EqualFold(c.ServerSideEncryption, "aws:kms") && c.SSEKMSKeyId == "" {
			return fmt.Errorf("%w: sse_kms_key_id is required when sse is 'aws:kms'", ErrConfig)
		}
	}

	// Validate and normalize ACL
	if c.ACL != "" {
		if !containsIgnoreCase(ValidACLs, c.ACL) {
			return fmt.Errorf("%w: invalid acl %q", ErrConfig, c.ACL)
		}
		c.ACL = strings.ToLower(c.ACL)
	}

	// Validate concurrency (must be >= 1 if specified, 0 means use default)
	if c.Concurrency < 0 {
		return fmt.Errorf("%w: concurrency must be >= 0", ErrConfig)
	}

	// Validate part size (must be >= 5 MB if specified, 0 means use default)
	if c.PartSize != 0 && c.PartSize < 5 {
		return fmt.Errorf("%w: part_size must be >= 5 MB", ErrConfig)
	}

	// Validate max_keys (must be >= 1 if specified, 0 means use default)
	if c.MaxKeys < 0 {
		return fmt.Errorf("%w: max_keys must be >= 0", ErrConfig)
	}

	return nil
}

func containsIgnoreCase(slice []string, item string) bool {
	for _, s := range slice {
		if strings.EqualFold(s, item) {
			return true
		}
	}
	return false
}

var configSchema = &jsonschema.Schema{
	Type: "object",
	// Note: bucket is not required here because it can come from DAG-level s3 config.
	// ValidateForOperation() checks for bucket at runtime after config merging.
	Properties: map[string]*jsonschema.Schema{
		// AWS Connection
		"region":            {Type: "string", Description: "AWS region (e.g., us-east-1)"},
		"endpoint":          {Type: "string", Description: "Custom S3-compatible endpoint URL"},
		"access_key_id":     {Type: "string", Description: "AWS access key ID"},
		"secret_access_key": {Type: "string", Description: "AWS secret access key"},
		"session_token":     {Type: "string", Description: "AWS session token (for temporary credentials)"},
		"profile":           {Type: "string", Description: "AWS credentials profile name"},
		"force_path_style":  {Type: "boolean", Description: "Use path-style addressing (for S3-compatible services)"},

		// Common
		"bucket":      {Type: "string", Description: "S3 bucket name (required)"},
		"key":         {Type: "string", Description: "S3 object key (required for upload/download/delete)"},
		"source":      {Type: "string", Description: "Local file path to upload (required for upload)"},
		"destination": {Type: "string", Description: "Local file path for download (required for download)"},

		// Upload options
		"content_type":  {Type: "string", Description: "Content-Type for the uploaded object"},
		"storage_class": {Type: "string", Description: "Storage class (STANDARD, STANDARD_IA, etc.)"},
		"metadata":      {Type: "object", Description: "Custom metadata key-value pairs"},

		// Server-side encryption
		"sse":            {Type: "string", Enum: []any{"AES256", "aws:kms"}, Description: "Server-side encryption type"},
		"sse_kms_key_id": {Type: "string", Description: "KMS key ID (required when sse is 'aws:kms')"},

		// Access control
		"acl": {Type: "string", Description: "Canned ACL (private, public-read, etc.)"},

		// Object tagging
		"tags": {Type: "object", Description: "Object tags as key-value pairs"},

		// Transfer options
		"part_size":   {Type: "integer", Minimum: new(float64(5)), Description: "Multipart upload part size in MB (default: 10, min: 5)"},
		"concurrency": {Type: "integer", Minimum: new(float64(1)), Description: "Number of concurrent upload/download parts (default: 5)"},

		// List options
		"prefix":        {Type: "string", Description: "Filter objects by key prefix"},
		"delimiter":     {Type: "string", Description: "Delimiter for grouping keys (e.g., '/')"},
		"max_keys":      {Type: "integer", Minimum: new(float64(1)), Description: "Maximum number of keys to return (default: 1000)"},
		"recursive":     {Type: "boolean", Description: "List all objects recursively (ignores delimiter)"},
		"output_format": {Type: "string", Enum: []any{"json", "jsonl"}, Description: "Output format: json (default) or jsonl"},

		// Delete options
		"quiet": {Type: "boolean", Description: "Suppress output for delete operation"},

		// Advanced
		"disable_ssl": {Type: "boolean", Description: "Disable SSL (for local testing only)"},
	},
}

func init() {
	core.RegisterExecutorConfigSchema("s3", configSchema)
}
