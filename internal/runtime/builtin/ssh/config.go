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
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/go-viper/mapstructure/v2"
	"github.com/google/jsonschema-go/jsonschema"
)

const defaultSSHTimeout = 30 * time.Second

// Config represents SSH connection info
type Config struct {
	User string
	Host string
	Port string
	// SSHAuth holds the key, password, and passphrase, each a literal or a
	// secret ref resolved at run start.
	ir.SSHAuth
	StrictHostKey bool           // Enable strict host key checking (defaults to true)
	KnownHostFile string         // Path to known_hosts file (defaults to ~/.ssh/known_hosts)
	Shell         string         // Shell for remote command execution (e.g., "/bin/bash")
	ShellArgs     []string       // Additional shell arguments (e.g., -e, -o pipefail)
	Timeout       time.Duration  // Connection timeout (defaults to 30s)
	Bastion       *BastionConfig // Optional bastion/jump host configuration
}

// BastionConfig represents bastion/jump host connection info
type BastionConfig struct {
	Host string
	Port string
	User string
	ir.SSHAuth
}

// sshMapConfig is the structure for decoding SSH config from a map.
type sshMapConfig struct {
	User          string
	IP            string
	Host          string
	Port          string
	Key           any
	Password      any
	Passphrase    any
	StrictHostKey bool   `mapstructure:"strict_host_key"`
	KnownHostFile string `mapstructure:"known_host_file"`
	Shell         string
	ShellArgs     []string `mapstructure:"shell_args"`
	Timeout       string
	Bastion       *sshMapBastion
}

// sshMapBastion is the structure for decoding a bastion config from a map.
type sshMapBastion struct {
	Host       string
	Port       string
	User       string
	Key        any
	Password   any
	Passphrase any
}

// FromMapConfig creates a client from a step's with block. Credentials given
// as secret refs are read from the values resolved at run start.
func FromMapConfig(ctx context.Context, mapCfg map[string]any) (*Client, error) {
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

	auth, err := ir.ParseSSHAuth("with", def.Key, def.Password, def.Passphrase)
	if err != nil {
		return nil, err
	}
	bastion, err := buildBastionFromMap(def.Bastion)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		User:          def.User,
		Host:          coalesce(def.Host, def.IP),
		Port:          def.Port,
		SSHAuth:       auth,
		StrictHostKey: def.StrictHostKey,
		KnownHostFile: def.KnownHostFile,
		Shell:         shell,
		ShellArgs:     shellArgs,
		Timeout:       timeout,
		Bastion:       bastion,
	}

	return NewClient(ctx, cfg)
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
func buildBastionFromMap(b *sshMapBastion) (*BastionConfig, error) {
	if b == nil {
		return nil, nil
	}
	auth, err := ir.ParseSSHAuth("with.bastion", b.Key, b.Password, b.Passphrase)
	if err != nil {
		return nil, err
	}
	port := b.Port
	if port == "" {
		port = "22"
	}
	return &BastionConfig{
		Host:    b.Host,
		Port:    port,
		User:    b.User,
		SSHAuth: auth,
	}, nil
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

// credentialSchema accepts a credential as a string or an object without
// describing the object. ir.ParseSSHAuth checks its shape and reports errors
// by field.
func credentialSchema(description string) *jsonschema.Schema {
	return &jsonschema.Schema{Types: []string{"string", "object"}, Description: description}
}

var bastionSchema = &jsonschema.Schema{
	Type:                 "object",
	Description:          "Bastion/jump host configuration",
	AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	Properties: map[string]*jsonschema.Schema{
		"host":       {Type: "string", Description: "Bastion host address"},
		"port":       portValueSchema("Bastion SSH port"),
		"user":       {Type: "string", Description: "Bastion SSH username"},
		"key":        credentialSchema("Bastion private key: a path, {file: <path>}, or {secret: <ref>}"),
		"password":   credentialSchema("Bastion SSH password: a string or {secret: <ref>}"),
		"passphrase": credentialSchema("Passphrase of the bastion's encrypted key: a string or {secret: <ref>}"),
	},
}

var configSchema = &jsonschema.Schema{
	Type:                 "object",
	AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	Properties: map[string]*jsonschema.Schema{
		"user":            {Type: "string", Description: "SSH username"},
		"host":            {Type: "string", Description: "SSH hostname"},
		"ip":              {Type: "string", Description: "SSH host IP (alias for host)"},
		"port":            portValueSchema("SSH port"),
		"key":             credentialSchema("Private key: a path, {file: <path>}, or {secret: <ref>}"),
		"password":        credentialSchema("SSH password: a string or {secret: <ref>}"),
		"passphrase":      credentialSchema("Passphrase of an encrypted key: a string or {secret: <ref>}"),
		"strict_host_key": {Type: "boolean", Description: "Enable strict host key checking"},
		"known_host_file": {Type: "string", Description: "Path to known_hosts file"},
		"shell":           {Type: "string", Description: "Shell for remote execution"},
		"shell_args":      {Type: "array", Items: &jsonschema.Schema{Type: "string"}, Description: "Additional shell arguments"},
		"timeout":         {Type: "string", Description: "Connection timeout (e.g., '30s', '1m')"},
		"bastion":         bastionSchema,
	},
}

var sftpConfigSchema = &jsonschema.Schema{
	Type:                 "object",
	AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	Properties: map[string]*jsonschema.Schema{
		"user":            {Type: "string", Description: "SSH username"},
		"host":            {Type: "string", Description: "SSH hostname"},
		"ip":              {Type: "string", Description: "SSH host IP (alias for host)"},
		"port":            portValueSchema("SSH port"),
		"key":             credentialSchema("Private key: a path, {file: <path>}, or {secret: <ref>}"),
		"password":        credentialSchema("SSH password: a string or {secret: <ref>}"),
		"passphrase":      credentialSchema("Passphrase of an encrypted key: a string or {secret: <ref>}"),
		"strict_host_key": {Type: "boolean", Description: "Enable strict host key checking"},
		"known_host_file": {Type: "string", Description: "Path to known_hosts file"},
		"timeout":         {Type: "string", Description: "Connection timeout (e.g., '30s', '1m')"},
		"direction":       {Type: "string", Description: "Transfer direction: 'upload' or 'download'"},
		"source":          {Type: "string", Description: "Source path (local for upload, remote for download)"},
		"destination":     {Type: "string", Description: "Destination path (remote for upload, local for download)"},
		"bastion":         bastionSchema,
	},
}

func init() {
	registry.RegisterExecutorConfigSchema("ssh", configSchema)
	registry.RegisterExecutorConfigSchema("sftp", sftpConfigSchema)
}
