// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/user"

	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/persis"
	fileaudit "github.com/dagucloud/dagu/v2/internal/persis/file/audit"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/secret"
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
	"github.com/dagucloud/dagu/v2/internal/workspace"
	"github.com/spf13/cobra"
)

// secretGlobalWorkspaceName is how the CLI names the global secret scope.
const secretGlobalWorkspaceName = "global"

// secretResolveAuditAction is the audit action for a plaintext read by the CLI.
const secretResolveAuditAction = "secret_resolve"

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

The value is written to stdout exactly, without a trailing newline. A
workspace that is neither registered nor holding any secret is rejected.
When audit logging is enabled, each read is recorded in the audit log, and
the value is not printed if the entry cannot be written.`,
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
		secrets := ctx.settings.SecretStore(ctx)
		if secrets == nil {
			return fmt.Errorf("secret store is not configured")
		}
		if err := ensureSecretWorkspaceKnown(ctx, secrets, workspaceName); err != nil {
			return err
		}
		sec, value, err := secret.NewReferenceResolver(secrets, workspaceName).
			ReadReference(ctx, secretref.Ref{Ref: ref})
		if err != nil {
			return fmt.Errorf("failed to resolve secret %q: %w", ref, err)
		}
		if err := auditSecretResolve(ctx, secrets, sec); err != nil {
			return fmt.Errorf("failed to record secret read in the audit log: %w", err)
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

// ensureSecretWorkspaceKnown rejects a workspace that is neither registered nor
// scoping any secret, so a mistyped name cannot silently resolve from global.
func ensureSecretWorkspaceKnown(ctx *Context, secrets secret.Store, name string) error {
	if name == secret.GlobalWorkspace {
		return nil
	}
	workspaces, err := store.NewWorkspaceStore(ctx.backend.Collection(persis.CollectionWorkspaces))
	if err != nil {
		return err
	}
	_, err = workspaces.GetByName(ctx, name)
	switch {
	case err == nil:
		return nil
	case !errors.Is(err, workspace.ErrWorkspaceNotFound):
		return err
	}
	scoped, err := secrets.List(ctx, secret.ListOptions{Workspace: &name})
	if err != nil {
		return err
	}
	if len(scoped) == 0 {
		return fmt.Errorf("%w: %q", workspace.ErrWorkspaceNotFound, name)
	}
	return nil
}

// auditSecretResolve records a plaintext read of sec, masked with the values
// in secrets, so reads outside DAG runs stay traceable. It records nothing
// when audit logging is disabled.
func auditSecretResolve(ctx *Context, secrets secret.Store, sec *secret.Secret) error {
	if !ctx.Config.Server.Audit.Enabled {
		return nil
	}
	auditStore, err := fileaudit.New(fileaudit.Dir(ctx.Config.Paths.AdminLogsDir), 0,
		fileaudit.WithMasker(audit.NewMasker(secrets)))
	if err != nil {
		return err
	}
	workspaceName := sec.Workspace
	if secret.IsGlobalWorkspace(workspaceName) {
		workspaceName = secretGlobalWorkspaceName
	}
	details, err := json.Marshal(map[string]any{
		"id":              sec.ID,
		"workspace":       workspaceName,
		"ref":             sec.Ref,
		"current_version": sec.CurrentVersion,
	})
	if err != nil {
		return err
	}
	username, userID := localOSSubject(user.Current)
	entry := audit.NewEntry(audit.CategorySecret, secretResolveAuditAction, userID, username).
		WithDetails(string(details))
	entry.Source = "cli"
	entry.ResourceType = "secret"
	entry.ResourceID = sec.ID
	entry.Workspace = workspaceName
	return auditStore.Append(ctx, entry)
}
