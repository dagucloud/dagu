// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package git

import (
	"fmt"
	"strings"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/go-viper/mapstructure/v2"
	"github.com/google/jsonschema-go/jsonschema"
)

type config struct {
	Repository    string `mapstructure:"repository"`
	Ref           string `mapstructure:"ref"`
	Path          string `mapstructure:"path"`
	Depth         int    `mapstructure:"depth"`
	Force         bool   `mapstructure:"force"`
	Token         string `mapstructure:"token"`
	Username      string `mapstructure:"username"`
	Password      string `mapstructure:"password"`
	SSHKeyPath    string `mapstructure:"ssh_key_path"`
	SSHPassphrase string `mapstructure:"ssh_passphrase"`
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

func validateConfig(operation string, cfg config) error {
	if operation != opCheckout {
		return fmt.Errorf("git: unsupported operation %q", operation)
	}
	if strings.TrimSpace(cfg.Repository) == "" {
		return fmt.Errorf("git: repository is required")
	}
	if strings.TrimSpace(cfg.Path) == "" {
		return fmt.Errorf("git: path is required")
	}
	if cfg.Depth < 0 {
		return fmt.Errorf("git: depth must be >= 0")
	}
	if cfg.SSHKeyPath != "" && (cfg.Token != "" || cfg.Password != "") {
		return fmt.Errorf("git: ssh_key_path cannot be combined with token or password")
	}
	if cfg.Token != "" && (cfg.Username != "" || cfg.Password != "") {
		return fmt.Errorf("git: token cannot be combined with username/password")
	}
	return nil
}

var configSchema = &jsonschema.Schema{
	Type:                 "object",
	AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	Properties: map[string]*jsonschema.Schema{
		"repository":     {Type: "string", Description: "Git repository URL or local repository path for action: git.checkout."},
		"ref":            {Type: "string", Description: "Branch, tag, or commit to checkout. Defaults to the repository default HEAD."},
		"path":           {Type: "string", Description: "Destination checkout path. Relative paths resolve from the step working directory."},
		"depth":          {Type: "integer", Minimum: new(float64(0)), Description: "Shallow clone/fetch depth. Zero means full history."},
		"force":          {Type: "boolean", Description: "Force checkout when the existing worktree has local changes. Defaults to false."},
		"token":          {Type: "string", Description: "HTTPS token for repository authentication."},
		"username":       {Type: "string", Description: "HTTPS username when using password authentication."},
		"password":       {Type: "string", Description: "HTTPS password for repository authentication."},
		"ssh_key_path":   {Type: "string", Description: "Path to an SSH private key for repository authentication."},
		"ssh_passphrase": {Type: "string", Description: "Passphrase for ssh_key_path."},
	},
}

func init() {
	core.RegisterExecutorConfigSchema(executorType, configSchema)
}
