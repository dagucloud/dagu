// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package redis

import (
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/google/jsonschema-go/jsonschema"
)

// pipelineCommandSchema defines the schema for pipeline commands.
var pipelineCommandSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"command": {Type: "string", Description: "Redis command"},
		"key":     {Type: "string", Description: "Key for the command"},
		"keys":    {Type: "array", Items: &jsonschema.Schema{Type: "string"}, Description: "Multiple keys"},
		"value":   {Description: "Value for the command"},
		"values":  {Type: "array", Description: "Multiple values"},
		"field":   {Type: "string", Description: "Hash field"},
		"fields":  {Type: "object", Description: "Multiple hash fields"},
		"ttl":     {Type: "integer", Description: "TTL in seconds"},
		"nx":      {Type: "boolean", Description: "Only set if not exists"},
		"xx":      {Type: "boolean", Description: "Only set if exists"},
		"score":   {Type: "number", Description: "Score for sorted set"},
	},
	Required: []string{"command"},
}

// configSchema defines the JSON schema for Redis executor configuration.
var configSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		// Connection - Basic
		"url":      {Type: "string", Format: "uri", Description: "Redis connection URL (redis://user:pass@host:port/db)"},
		"host":     {Type: "string", Description: "Redis host (alternative to url)"},
		"port":     {Type: "integer", Minimum: new(float64(1)), Maximum: new(float64(65535)), Description: "Redis port (default: 6379)"},
		"password": {Type: "string", Description: "Authentication password"},
		"username": {Type: "string", Description: "ACL username (Redis 6+)"},
		"db":       {Type: "integer", Minimum: new(float64(0)), Maximum: new(float64(15)), Description: "Database number (0-15)"},

		// Connection - TLS
		"tls":             {Type: "boolean", Description: "Enable TLS"},
		"tls_cert":        {Type: "string", Description: "Path to client certificate"},
		"tls_key":         {Type: "string", Description: "Path to client key"},
		"tls_ca":          {Type: "string", Description: "Path to CA certificate"},
		"tls_skip_verify": {Type: "boolean", Description: "Skip TLS verification"},

		// Connection - High Availability
		"mode":            {Type: "string", Enum: []any{"standalone", "sentinel", "cluster"}, Description: "Connection mode"},
		"sentinel_master": {Type: "string", Description: "Sentinel master name"},
		"sentinel_addrs":  {Type: "array", Items: &jsonschema.Schema{Type: "string"}, Description: "Sentinel addresses"},
		"cluster_addrs":   {Type: "array", Items: &jsonschema.Schema{Type: "string"}, Description: "Cluster node addresses"},

		// Connection - Retry
		"max_retries": {Type: "integer", Description: "Max retry attempts"},

		// Command Execution
		"command": {Type: "string", Description: "Redis command to execute"},
		"key":     {Type: "string", Description: "Primary key"},
		"keys":    {Type: "array", Items: &jsonschema.Schema{Type: "string"}, Description: "Multiple keys"},
		"value":   {Description: "Value for SET operations"},
		"values":  {Type: "array", Description: "Multiple values"},
		"field":   {Type: "string", Description: "Hash field"},
		"fields":  {Type: "object", AdditionalProperties: &jsonschema.Schema{}, Description: "Multiple hash fields"},

		// Command Options
		"ttl":      {Type: "integer", Description: "Expiration in seconds"},
		"nx":       {Type: "boolean", Description: "SET if not exists"},
		"xx":       {Type: "boolean", Description: "SET if exists"},
		"keep_ttl": {Type: "boolean", Description: "Preserve existing TTL"},
		"count":    {Type: "integer", Description: "Count for SCAN, LPOP, etc."},
		"match":    {Type: "string", Description: "Pattern for SCAN"},

		// List Options
		"position": {Type: "string", Enum: []any{"BEFORE", "AFTER"}, Description: "Position for LINSERT"},
		"pivot":    {Type: "string", Description: "Pivot for LINSERT"},
		"start":    {Type: "integer", Description: "Range start"},
		"stop":     {Type: "integer", Description: "Range stop"},

		// Sorted Set Options
		"score":       {Type: "number", Description: "Member score"},
		"min":         {Type: "string", Description: "Range min"},
		"max":         {Type: "string", Description: "Range max"},
		"with_scores": {Type: "boolean", Description: "Include scores in output"},

		// Pub/Sub Options
		"channel":  {Type: "string", Description: "Pub/Sub channel"},
		"channels": {Type: "array", Items: &jsonschema.Schema{Type: "string"}, Description: "Multiple channels"},
		"message":  {Description: "Message to publish"},

		// Stream Options
		"stream":        {Type: "string", Description: "Stream key"},
		"stream_id":     {Type: "string", Description: "Message ID (* for auto)"},
		"group":         {Type: "string", Description: "Consumer group"},
		"consumer":      {Type: "string", Description: "Consumer name"},
		"stream_fields": {Type: "object", Description: "Stream entry fields"},
		"max_len":       {Type: "integer", Description: "MAXLEN for XADD"},
		"block":         {Type: "integer", Description: "Block timeout in milliseconds"},
		"no_ack":        {Type: "boolean", Description: "NOACK for XREADGROUP"},

		// Scripting
		"script":      {Type: "string", Description: "Lua script"},
		"script_file": {Type: "string", Description: "Path to Lua script file"},
		"script_sha":  {Type: "string", Description: "Pre-loaded script SHA"},
		"script_keys": {Type: "array", Items: &jsonschema.Schema{Type: "string"}, Description: "KEYS for script"},
		"script_args": {Type: "array", Description: "ARGV for script"},

		// Pipeline/Transaction
		"pipeline": {Type: "array", Items: pipelineCommandSchema, Description: "Batch commands"},
		"watch":    {Type: "array", Items: &jsonschema.Schema{Type: "string"}, Description: "Keys to WATCH"},
		"multi":    {Type: "boolean", Description: "Use MULTI/EXEC transaction"},

		// Distributed Lock
		"lock":         {Type: "string", Description: "Lock name"},
		"lock_timeout": {Type: "integer", Description: "Lock expiry in seconds"},
		"lock_retry":   {Type: "integer", Description: "Lock retry attempts"},
		"lock_wait":    {Type: "integer", Description: "Wait between retries in milliseconds"},

		// Output
		"output_format": {Type: "string", Enum: []any{"json", "jsonl", "raw", "csv"}, Description: "Output format"},
		"null_value":    {Type: "string", Description: "String representation for nil values"},

		// Execution
		"timeout":         {Type: "integer", Description: "Command timeout in seconds"},
		"max_result_size": {Type: "integer", Description: "Max result size in bytes"},
	},
}

func init() {
	ir.RegisterExecutorConfigSchema("redis", configSchema)
}
