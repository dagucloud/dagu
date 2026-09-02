// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ssh

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/executor/registry"

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/go-viper/mapstructure/v2"
	"github.com/google/jsonschema-go/jsonschema"
)

const defaultSSHTimeout = 30 * time.Second

// Config represents SSH connection info
type Config struct {
	User          string
	Host          string
	Port          string
	Key           string
	Password      string
	StrictHostKey bool           // Enable strict host key checking (defaults to true)
	KnownHostFile string         // Path to known_hosts file (defaults to ~/.ssh/known_hosts)
	Shell         string         // Shell for remote command execution (e.g., "/bin/bash")
	ShellArgs     []string       // Additional shell arguments (e.g., -e, -o pipefail)
	Timeout       time.Duration  // Connection timeout (defaults to 30s)
	Bastion       *BastionConfig // Optional bastion/jump host configuration
}

// BastionConfig represents bastion/jump host connection info
type BastionConfig struct {
	Host     string
	Port     string
	User     string
	Key      string
	Password string
}

// sshMapConfig is the structure for decoding SSH config from a map.
type sshMapConfig struct {
	User          string
	IP            string
	Host          string
	Port          string
	Key           string
	Password      string
	StrictHostKey bool   `mapstructure:"strict_host_key"`
	KnownHostFile string `mapstructure:"known_host_file"`
	Shell         string
	ShellArgs     []string `mapstructure:"shell_args"`
	Timeout       string
	Bastion       *struct {
		Host     string
		Port     string
		User     string
		Key      string
		Password string
	}
}

func FromMapConfig(_ context.Context, mapCfg map[string]any) (*Client, error) {
	def := sshMapConfig{StrictHostKey: true}
	md, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:           &def,
		WeaklyTypedInput: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create decoder: %w", err)
	}
	if err := md.Decode(mapCfg); err != nil {
		return nil, fmt.Errorf("failed to decode ssh config: %w", err)
	}

	shell, shellArgs, err := parseShellConfig(def.Shell, def.ShellArgs)
	if err != nil {
		return nil, fmt.Errorf("failed to parse shell config: %w", err)
	}

	timeout, err := parseTimeout(def.Timeout)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		User:          def.User,
		Host:          coalesce(def.Host, def.IP),
		Port:          def.Port,
		Key:           def.Key,
		Password:      def.Password,
		StrictHostKey: def.StrictHostKey,
		KnownHostFile: def.KnownHostFile,
		Shell:         shell,
		ShellArgs:     shellArgs,
		Timeout:       timeout,
		Bastion:       buildBastionFromMap(def.Bastion),
	}

	return NewClient(cfg)
}

// parseTimeout parses a timeout string or returns the default.
func parseTimeout(s string) (time.Duration, error) {
	if s == "" {
		return defaultSSHTimeout, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid timeout duration %q: %w", s, err)
	}
	return d, nil
}

// buildBastionFromMap converts a bastion map config to BastionConfig.
func buildBastionFromMap(b *struct {
	Host     string
	Port     string
	User     string
	Key      string
	Password string
}) *BastionConfig {
	if b == nil {
		return nil
	}
	port := b.Port
	if port == "" {
		port = "22"
	}
	return &BastionConfig{
		Host:     b.Host,
		Port:     port,
		User:     b.User,
		Key:      b.Key,
		Password: b.Password,
	}
}

// coalesce returns the first non-empty string.
func coalesce(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func parseShellConfig(shell string, args []string) (string, []string, error) {
	shell = strings.TrimSpace(shell)
	if shell == "" {
		return "", slices.Clone(args), nil
	}

	parsedShell, parsedArgs, err := cmdutil.SplitCommand(shell)
	if err != nil {
		return "", nil, err
	}

	allArgs := append(parsedArgs, args...)
	if len(allArgs) == 0 {
		return strings.TrimSpace(parsedShell), nil, nil
	}
	return strings.TrimSpace(parsedShell), allArgs, nil
}

func portValueSchema(description string) *jsonschema.Schema {
	return &jsonschema.Schema{
		Description: description,
		OneOf: []*jsonschema.Schema{
			{Type: "string"},
			{Type: "integer"},
		},
	}
}

var configSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"user":            {Type: "string", Description: "SSH username"},
		"host":            {Type: "string", Description: "SSH hostname"},
		"ip":              {Type: "string", Description: "SSH host IP (alias for host)"},
		"port":            portValueSchema("SSH port"),
		"key":             {Type: "string", Description: "Path to private key file"},
		"password":        {Type: "string", Description: "SSH password"},
		"strict_host_key": {Type: "boolean", Description: "Enable strict host key checking"},
		"known_host_file": {Type: "string", Description: "Path to known_hosts file"},
		"shell":           {Type: "string", Description: "Shell for remote execution"},
		"shell_args":      {Type: "array", Items: &jsonschema.Schema{Type: "string"}, Description: "Additional shell arguments"},
		"timeout":         {Type: "string", Description: "Connection timeout (e.g., '30s', '1m')"},
		"bastion": {
			Type:        "object",
			Description: "Bastion/jump host configuration",
			Properties: map[string]*jsonschema.Schema{
				"host":     {Type: "string", Description: "Bastion host address"},
				"port":     portValueSchema("Bastion SSH port"),
				"user":     {Type: "string", Description: "Bastion SSH username"},
				"key":      {Type: "string", Description: "Path to bastion private key file"},
				"password": {Type: "string", Description: "Bastion SSH password"},
			},
		},
	},
}

var sftpConfigSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"user":            {Type: "string", Description: "SSH username"},
		"host":            {Type: "string", Description: "SSH hostname"},
		"ip":              {Type: "string", Description: "SSH host IP (alias for host)"},
		"port":            portValueSchema("SSH port"),
		"key":             {Type: "string", Description: "Path to private key file"},
		"password":        {Type: "string", Description: "SSH password"},
		"strict_host_key": {Type: "boolean", Description: "Enable strict host key checking"},
		"known_host_file": {Type: "string", Description: "Path to known_hosts file"},
		"timeout":         {Type: "string", Description: "Connection timeout (e.g., '30s', '1m')"},
		"direction":       {Type: "string", Description: "Transfer direction: 'upload' or 'download'"},
		"source":          {Type: "string", Description: "Source path (local for upload, remote for download)"},
		"destination":     {Type: "string", Description: "Destination path (remote for upload, local for download)"},
		"bastion": {
			Type:        "object",
			Description: "Bastion/jump host configuration",
			Properties: map[string]*jsonschema.Schema{
				"host":     {Type: "string", Description: "Bastion host address"},
				"port":     portValueSchema("Bastion SSH port"),
				"user":     {Type: "string", Description: "Bastion SSH username"},
				"key":      {Type: "string", Description: "Path to bastion private key file"},
				"password": {Type: "string", Description: "Bastion SSH password"},
			},
		},
	},
}

func init() {
	registry.RegisterExecutorConfigSchema("ssh", configSchema)
	registry.RegisterExecutorConfigSchema("sftp", sftpConfigSchema)
}
