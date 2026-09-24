// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"io"

	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/secret"
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
	"github.com/dagucloud/dagu/v2/internal/workspace"
	"github.com/spf13/cobra"
)

// secretGlobalWorkspaceName is how the CLI names the global secret scope.
const secretGlobalWorkspaceName = "global"

var secretWorkspaceFlag = commandLineFlag{
	name:  "workspace",
	usage: "Workspace to resolve the secret in; falls back to global, as a DAG run does (default: global)",
}

// Secret returns the command group for the secret registry.
func Secret() *cobra.Command {
	cmd := NewCommand(&cobra.Command{
		Use:   "secret",
		Short: "Read secrets from the secret registry",
	}, nil, func(ctx *Context, _ []string) error {
		return ctx.Command.Help()
	})

	cmd.AddCommand(secretResolveCommand())
	return cmd
}

func secretResolveCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "resolve <ref>",
		Short: "Print a secret's plaintext value",
		Long: `Print the current plaintext value of a registry secret, resolved the same
way as a DAG's secrets entry with that ref.

The value is written to stdout exactly, without a trailing newline.`,
		Args: cobra.ExactArgs(1),
	}, []commandLineFlag{secretWorkspaceFlag}, func(ctx *Context, args []string) error {
		ref := args[0]
		if err := secret.ValidateRef(ref); err != nil {
			return err
		}
		raw, err := ctx.StringParam(secretWorkspaceFlag.name)
		if err != nil {
			return err
		}
		workspaceName, err := secretWorkspace(raw)
		if err != nil {
			return err
		}
		store := file.NewSecretStore(ctx, ctx.Config, ctx.backend.Collection(persis.CollectionSecrets))
		if store == nil {
			return fmt.Errorf("secret store is not configured")
		}
		value, err := secret.NewReferenceResolver(store, workspaceName).
			ResolveReference(ctx, secretref.Ref{Ref: ref})
		if err != nil {
			return fmt.Errorf("failed to resolve secret %q: %w", ref, err)
		}
		_, err = io.WriteString(ctx.Command.OutOrStdout(), value)
		return err
	})
}

// secretWorkspace maps the workspace flag onto the scope the registry stores.
func secretWorkspace(name string) (string, error) {
	if name == "" || name == secretGlobalWorkspaceName {
		return secret.GlobalWorkspace, nil
	}
	if err := workspace.ValidateName(name); err != nil {
		return "", err
	}
	return name, nil
}
