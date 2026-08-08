// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package git

import (
	"fmt"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/ir"
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

type worktreeConfig struct {
	Branch            string
	Path              string
	Base              string
	HasBranch         bool
	HasPath           bool
	HasBase           bool
	CreateBranch      bool
	Force             bool
	DeleteBranch      bool
	ForceDeleteBranch bool
}

func decodeWorktreeConfig(operation string, raw map[string]any) (worktreeConfig, error) {
	if raw == nil {
		raw = map[string]any{}
	}
	allowed := map[string]bool{}
	switch operation {
	case opWorktreeAdd:
		for _, name := range []string{"branch", "path", "create_branch", "base"} {
			allowed[name] = true
		}
	case opWorktreeRemove:
		for _, name := range []string{"branch", "path", "force", "delete_branch", "force_delete_branch"} {
			allowed[name] = true
		}
	default:
		return worktreeConfig{}, fmt.Errorf("git: unsupported operation %q", operation)
	}
	for name := range raw {
		if !allowed[name] {
			return worktreeConfig{}, fmt.Errorf("git %s: unsupported field %q", operation, name)
		}
	}

	cfg := worktreeConfig{}
	var err error
	if cfg.Branch, cfg.HasBranch, err = worktreeString(raw, "branch"); err != nil {
		return worktreeConfig{}, err
	}
	if cfg.Path, cfg.HasPath, err = worktreeString(raw, "path"); err != nil {
		return worktreeConfig{}, err
	}
	if cfg.Base, cfg.HasBase, err = worktreeString(raw, "base"); err != nil {
		return worktreeConfig{}, err
	}
	if cfg.CreateBranch, err = worktreeBool(raw, "create_branch"); err != nil {
		return worktreeConfig{}, err
	}
	if cfg.Force, err = worktreeBool(raw, "force"); err != nil {
		return worktreeConfig{}, err
	}
	if cfg.DeleteBranch, err = worktreeBool(raw, "delete_branch"); err != nil {
		return worktreeConfig{}, err
	}
	if cfg.ForceDeleteBranch, err = worktreeBool(raw, "force_delete_branch"); err != nil {
		return worktreeConfig{}, err
	}

	if operation == opWorktreeAdd {
		if cfg.HasBranch && cfg.HasBase && !cfg.CreateBranch {
			return worktreeConfig{}, fmt.Errorf("git %s: base requires create_branch when branch is specified", operation)
		}
	} else {
		if !cfg.HasBranch && !cfg.HasPath {
			return worktreeConfig{}, fmt.Errorf("git %s: branch or path is required", operation)
		}
		if cfg.DeleteBranch && !cfg.HasBranch {
			return worktreeConfig{}, fmt.Errorf("git %s: delete_branch requires branch", operation)
		}
		if cfg.ForceDeleteBranch && !cfg.DeleteBranch {
			return worktreeConfig{}, fmt.Errorf("git %s: force_delete_branch requires delete_branch", operation)
		}
	}
	return cfg, nil
}

func worktreeString(raw map[string]any, name string) (string, bool, error) {
	value, ok := raw[name]
	if !ok {
		return "", false, nil
	}
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return "", false, fmt.Errorf("git worktree: %s must be a non-empty string", name)
	}
	return strings.TrimSpace(text), true, nil
}

func worktreeBool(raw map[string]any, name string) (bool, error) {
	value, ok := raw[name]
	if !ok {
		return false, nil
	}
	flag, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("git worktree: %s must be a boolean", name)
	}
	return flag, nil
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
		"repository":          {Type: "string", Description: "Git repository URL or local repository path for action: git.checkout."},
		"branch":              {Type: "string", Description: "Local branch for a Git worktree action."},
		"ref":                 {Type: "string", Description: "Branch, tag, or commit to checkout. Defaults to the repository default HEAD."},
		"path":                {Type: "string", Description: "Destination checkout path. Relative paths resolve from the step working directory."},
		"depth":               {Type: "integer", Minimum: new(float64(0)), Description: "Shallow clone/fetch depth. Zero means full history."},
		"force":               {Type: "boolean", Description: "Force checkout when the existing worktree has local changes. Defaults to false."},
		"create_branch":       {Type: "boolean", Description: "Permit creation of an explicitly named worktree branch."},
		"base":                {Type: "string", Description: "Local base revision for a new worktree branch."},
		"delete_branch":       {Type: "boolean", Description: "Delete the local branch after removing its worktree."},
		"force_delete_branch": {Type: "boolean", Description: "Permit deletion of an unmerged local branch."},
		"token":               {Type: "string", Description: "HTTPS token for repository authentication."},
		"username":            {Type: "string", Description: "HTTPS username when using password authentication."},
		"password":            {Type: "string", Description: "HTTPS password for repository authentication."},
		"ssh_key_path":        {Type: "string", Description: "Path to an SSH private key for repository authentication."},
		"ssh_passphrase":      {Type: "string", Description: "Passphrase for ssh_key_path."},
	},
}

func init() {
	ir.RegisterExecutorConfigSchema(executorType, configSchema)
}
