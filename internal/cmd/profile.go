// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/dagucloud/dagu/internal/profile"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const profileCLIActor = "cli"

var (
	profileDescriptionFlag = commandLineFlag{name: "description", usage: "Profile description"}
	profileProtectedFlag   = commandLineFlag{name: "protected", usage: "Mark the profile as protected", isBool: true}
	profileValueStdinFlag  = commandLineFlag{name: "value-stdin", usage: "Read the secret value from stdin", isBool: true}
)

func Profile() *cobra.Command {
	cmd := NewCommand(&cobra.Command{
		Use:   "profile",
		Short: "Manage runtime profiles",
	}, nil, func(ctx *Context, _ []string) error {
		return ctx.Command.Help()
	})

	cmd.AddCommand(profileListCommand())
	cmd.AddCommand(profileShowCommand())
	cmd.AddCommand(profileCreateCommand())
	cmd.AddCommand(profileEnableCommand())
	cmd.AddCommand(profileDisableCommand())
	cmd.AddCommand(profileDeleteCommand())
	cmd.AddCommand(profileSetVarCommand())
	cmd.AddCommand(profileSetSecretCommand())
	cmd.AddCommand(profileDeleteKeyCommand())
	return cmd
}

func profileListCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "list",
		Short: "List runtime profiles",
	}, nil, func(ctx *Context, _ []string) error {
		store, err := runtimeProfileStore(ctx)
		if err != nil {
			return err
		}
		profiles, err := store.List(ctx)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		if _, err := fmt.Fprintln(w, "NAME\tSTATUS\tPROTECTED\tVARIABLES\tSECRETS\tDESCRIPTION"); err != nil {
			return err
		}
		for _, item := range profiles {
			var variables, secrets int
			for _, entry := range item.Entries {
				switch entry.Kind {
				case profile.EntryKindVariable:
					variables++
				case profile.EntryKindSecret:
					secrets++
				}
			}
			if _, err := fmt.Fprintf(w, "%s\t%s\t%t\t%d\t%d\t%s\n",
				item.Name, item.Status, item.Protected, variables, secrets, item.Description); err != nil {
				return err
			}
		}
		return w.Flush()
	})
}

func profileShowCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "show <profile>",
		Short: "Show runtime profile metadata",
		Args:  cobra.ExactArgs(1),
	}, nil, func(ctx *Context, args []string) error {
		item, err := getRuntimeProfile(ctx, args[0])
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		if _, err := fmt.Fprintf(w, "Name\t%s\nStatus\t%s\nProtected\t%t\nDescription\t%s\n",
			item.Name, item.Status, item.Protected, item.Description); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w, "\nKEY\tKIND\tVALUE"); err != nil {
			return err
		}
		for _, entry := range item.Entries {
			value := entry.Value
			if entry.Kind == profile.EntryKindSecret {
				value = "********"
			}
			if _, err := fmt.Fprintf(w, "%s\t%s\t%s\n", entry.Key, entry.Kind, value); err != nil {
				return err
			}
		}
		return w.Flush()
	})
}

func profileCreateCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "create <profile>",
		Short: "Create a runtime profile",
		Args:  cobra.ExactArgs(1),
	}, []commandLineFlag{profileDescriptionFlag, profileProtectedFlag}, func(ctx *Context, args []string) error {
		store, err := runtimeProfileStore(ctx)
		if err != nil {
			return err
		}
		description, err := ctx.StringParam("description")
		if err != nil {
			return err
		}
		protected, err := ctx.Command.Flags().GetBool("protected")
		if err != nil {
			return err
		}
		item, err := profile.New(profile.CreateInput{
			Name:        args[0],
			Description: description,
			Protected:   protected,
			CreatedBy:   profileCLIActor,
		}, time.Now().UTC())
		if err != nil {
			return err
		}
		if err := store.Create(ctx, item); err != nil {
			return err
		}
		fmt.Println(item.Name)
		return nil
	})
}

func profileEnableCommand() *cobra.Command {
	return profileStatusCommand("enable", "Enable a runtime profile", profile.StatusActive)
}

func profileDisableCommand() *cobra.Command {
	return profileStatusCommand("disable", "Disable a runtime profile", profile.StatusDisabled)
}

func profileStatusCommand(use, short string, status profile.Status) *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   use + " <profile>",
		Short: short,
		Args:  cobra.ExactArgs(1),
	}, nil, func(ctx *Context, args []string) error {
		store, item, err := getRuntimeProfileForUpdate(ctx, args[0])
		if err != nil {
			return err
		}
		if err := item.SetStatus(status, profileCLIActor, time.Now().UTC()); err != nil {
			return err
		}
		if err := store.Update(ctx, item); err != nil {
			return err
		}
		fmt.Println(item.Name)
		return nil
	})
}

func profileDeleteCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "delete <profile>",
		Short: "Delete a runtime profile",
		Args:  cobra.ExactArgs(1),
	}, nil, func(ctx *Context, args []string) error {
		store, err := runtimeProfileStore(ctx)
		if err != nil {
			return err
		}
		if err := profile.ValidateName(args[0]); err != nil {
			return err
		}
		if err := store.Delete(ctx, args[0]); err != nil {
			return err
		}
		fmt.Println(args[0])
		return nil
	})
}

func profileSetVarCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "set-var <profile> <key> <value>",
		Short: "Set a runtime profile variable",
		Args:  cobra.ExactArgs(3),
	}, nil, func(ctx *Context, args []string) error {
		store, item, err := getRuntimeProfileForUpdate(ctx, args[0])
		if err != nil {
			return err
		}
		updated, err := profile.NewManager(store, nil).SetVariable(ctx, item, args[1], args[2], profileCLIActor)
		if err != nil {
			return err
		}
		fmt.Printf("%s %s\n", updated.Name, args[1])
		return nil
	})
}

func profileSetSecretCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "set-secret <profile> <key>",
		Short: "Set a runtime profile secret",
		Args:  cobra.ExactArgs(2),
	}, []commandLineFlag{profileValueStdinFlag}, func(ctx *Context, args []string) error {
		stores := ctx.agentStores()
		if stores.SecretStore == nil {
			return fmt.Errorf("secret store is not configured")
		}
		profileStore, item, err := getRuntimeProfileForUpdate(ctx, args[0])
		if err != nil {
			return err
		}
		value, err := runtimeProfileSecretValue(ctx)
		if err != nil {
			return err
		}
		updated, err := profile.NewManager(profileStore, stores.SecretStore).
			SetSecret(ctx, item, args[1], value, profileCLIActor)
		if err != nil {
			return err
		}
		fmt.Printf("%s %s\n", updated.Name, args[1])
		return nil
	})
}

func runtimeProfileSecretValue(ctx *Context) (string, error) {
	useStdin, err := ctx.Command.Flags().GetBool("value-stdin")
	if err != nil {
		return "", err
	}
	if useStdin {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("failed to read secret value from stdin: %w", err)
		}
		return strings.TrimRight(string(data), "\r\n"), nil
	}

	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", fmt.Errorf("secret value requires --value-stdin when stdin is not a terminal")
	}

	if _, err := fmt.Fprint(os.Stderr, "Secret value: "); err != nil {
		return "", err
	}
	value, err := term.ReadPassword(int(os.Stdin.Fd()))
	if _, printErr := fmt.Fprintln(os.Stderr); printErr != nil && err == nil {
		err = printErr
	}
	if err != nil {
		return "", fmt.Errorf("failed to read secret value: %w", err)
	}
	return string(value), nil
}

func profileDeleteKeyCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "delete-key <profile> <key>",
		Short: "Delete a runtime profile entry",
		Args:  cobra.ExactArgs(2),
	}, nil, func(ctx *Context, args []string) error {
		store, item, err := getRuntimeProfileForUpdate(ctx, args[0])
		if err != nil {
			return err
		}
		if err := profile.NewManager(store, nil).DeleteEntry(ctx, item, args[1], profileCLIActor); err != nil {
			return err
		}
		fmt.Printf("%s %s\n", item.Name, args[1])
		return nil
	})
}

func runtimeProfileStore(ctx *Context) (profile.Store, error) {
	if ctx.IsRemote() {
		return nil, fmt.Errorf("profile command is not supported with --context")
	}
	stores := ctx.agentStores()
	if stores.ProfileStore == nil {
		return nil, fmt.Errorf("profile store is not configured")
	}
	return stores.ProfileStore, nil
}

func getRuntimeProfile(ctx *Context, name string) (*profile.Profile, error) {
	_, item, err := getRuntimeProfileForUpdate(ctx, name)
	return item, err
}

func getRuntimeProfileForUpdate(ctx *Context, name string) (profile.Store, *profile.Profile, error) {
	store, err := runtimeProfileStore(ctx)
	if err != nil {
		return nil, nil, err
	}
	if err := profile.ValidateName(name); err != nil {
		return nil, nil, err
	}
	item, err := store.GetByName(ctx, name)
	if errors.Is(err, profile.ErrNotFound) {
		return nil, nil, fmt.Errorf("profile %q not found", name)
	}
	if err != nil {
		return nil, nil, err
	}
	return store, item, nil
}
