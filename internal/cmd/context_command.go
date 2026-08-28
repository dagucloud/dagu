// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"

	"golang.org/x/term"

	"github.com/spf13/cobra"
)

func ContextCommand() *cobra.Command {
	cmd := NewCommand(&cobra.Command{
		Use:   "context",
		Short: "Manage CLI contexts for local and remote Dagu servers",
	}, nil, func(cmd *Context, _ []string) error {
		return cmd.Command.Help()
	})

	cmd.AddCommand(contextListCommand())
	cmd.AddCommand(contextAddCommand())
	cmd.AddCommand(contextUpdateCommand())
	cmd.AddCommand(contextRemoveCommand())
	cmd.AddCommand(contextUseCommand())
	cmd.AddCommand(contextTestCommand())
	return cmd
}

var contextManageFlags = []commandLineFlag{
	contextServerFlag,
	contextAPIKeyFlag,
	contextDescriptionFlag,
	contextSkipTLSVerifyFlag,
	contextTimeoutFlag,
}

var (
	contextServerFlag        = commandLineFlag{name: "server", usage: "API base URL of the remote Dagu server (for example, https://dagu.example.com/api/v1)"}
	contextAPIKeyFlag        = commandLineFlag{name: "api-key", usage: "API key to use for the context"}
	contextDescriptionFlag   = commandLineFlag{name: "description", usage: "Optional human-readable description"}
	contextSkipTLSVerifyFlag = commandLineFlag{name: "skip-tls-verify", usage: "Skip TLS certificate verification", isBool: true}
	contextTimeoutFlag       = commandLineFlag{name: "timeout", usage: "HTTP timeout in seconds"}
)

func contextListCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "list",
		Short: "List available CLI contexts",
	}, nil, func(ctx *Context, _ []string) error {
		contexts, err := ctx.ContextStore.List(ctx)
		if err != nil {
			return err
		}
		current, err := ctx.ContextStore.Current(ctx)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		if _, err := fmt.Fprintln(w, "CURRENT\tNAME\tSERVER\tDESCRIPTION"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", currentMarker(current == localContextName), localContextName, "-", "Built-in local context"); err != nil {
			return err
		}
		for _, item := range contexts {
			if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", currentMarker(current == item.Name), item.Name, item.ServerURL, item.Description); err != nil {
				return err
			}
		}
		return w.Flush()
	})
}

func contextAddCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "add <name>",
		Short: "Add a remote CLI context",
		Args:  cobra.ExactArgs(1),
	}, contextManageFlags, func(ctx *Context, args []string) error {
		item, err := readContextInput(ctx, args[0], false)
		if err != nil {
			return err
		}
		return ctx.ContextStore.Create(ctx, item)
	})
}

func contextUpdateCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "update <name>",
		Short: "Update a remote CLI context",
		Args:  cobra.ExactArgs(1),
	}, contextManageFlags, func(ctx *Context, args []string) error {
		current, err := ctx.ContextStore.Get(ctx, args[0])
		if err != nil {
			return err
		}
		item, err := readContextInput(ctx, args[0], true)
		if err != nil {
			return err
		}
		if !ctx.Command.Flags().Changed("server") {
			item.ServerURL = current.ServerURL
		} else if item.ServerURL == "" {
			return fmt.Errorf("--server cannot be empty")
		}
		if !ctx.Command.Flags().Changed("api-key") {
			item.APIKey = current.APIKey
		} else if item.APIKey == "" {
			return fmt.Errorf("--api-key cannot be empty")
		}
		if !ctx.Command.Flags().Changed("description") {
			item.Description = current.Description
		}
		if !ctx.Command.Flags().Changed("skip-tls-verify") {
			item.SkipTLSVerify = current.SkipTLSVerify
		}
		if !ctx.Command.Flags().Changed("timeout") {
			item.TimeoutSeconds = current.TimeoutSeconds
		}
		return ctx.ContextStore.Update(ctx, item)
	})
}

func contextRemoveCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a remote CLI context",
		Args:  cobra.ExactArgs(1),
	}, nil, func(ctx *Context, args []string) error {
		return ctx.ContextStore.Delete(ctx, args[0])
	})
}

func contextUseCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "use <name|local>",
		Short: "Set the current CLI context",
		Args:  cobra.ExactArgs(1),
	}, nil, func(ctx *Context, args []string) error {
		return ctx.ContextStore.Use(ctx, args[0])
	})
}

func contextTestCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "test <name|local>",
		Short: "Test connectivity for a CLI context",
		Args:  cobra.ExactArgs(1),
	}, nil, func(ctx *Context, args []string) error {
		name := args[0]
		item, err := ctx.ContextStore.Get(ctx, name)
		if err != nil {
			return err
		}
		if item.Name == localContextName {
			fmt.Println("local context is always available")
			return nil
		}
		remote, err := newRemoteClient(item)
		if err != nil {
			return err
		}
		if err := remote.Test(ctx); err != nil {
			return err
		}
		fmt.Printf("context %q is reachable\n", name)
		return nil
	})
}

func readContextInput(ctx *Context, name string, allowPartial bool) (*cliContext, error) {
	server, err := ctx.StringParam("server")
	if err != nil {
		return nil, err
	}
	apiKey, err := ctx.StringParam("api-key")
	if err != nil {
		return nil, err
	}
	description, err := ctx.StringParam("description")
	if err != nil {
		return nil, err
	}
	timeout, err := parseContextTimeout(ctx)
	if err != nil {
		return nil, err
	}
	skipTLSVerify, err := ctx.Command.Flags().GetBool("skip-tls-verify")
	if err != nil {
		return nil, err
	}
	if apiKey == "" && !allowPartial && term.IsTerminal(int(os.Stdin.Fd())) {
		if _, err := fmt.Fprint(os.Stderr, "API key: "); err != nil {
			return nil, err
		}
		bytes, err := term.ReadPassword(int(os.Stdin.Fd()))
		if _, printErr := fmt.Fprintln(os.Stderr); printErr != nil {
			return nil, printErr
		}
		if err != nil {
			return nil, err
		}
		apiKey = string(bytes)
	}
	item := &cliContext{
		Name:           name,
		ServerURL:      server,
		APIKey:         apiKey,
		Description:    description,
		SkipTLSVerify:  skipTLSVerify,
		TimeoutSeconds: timeout,
	}
	if allowPartial {
		return item, nil
	}
	if item.ServerURL == "" {
		return nil, fmt.Errorf("--server is required")
	}
	if item.APIKey == "" {
		return nil, fmt.Errorf("--api-key is required")
	}
	return item, ctx.ContextStore.ValidateContext(item)
}

func parseContextTimeout(ctx *Context) (int, error) {
	timeoutValue, err := ctx.StringParam("timeout")
	if err != nil {
		return 0, err
	}
	if timeoutValue == "" {
		return 0, nil
	}
	timeout, err := strconv.Atoi(timeoutValue)
	if err != nil {
		return 0, fmt.Errorf("invalid --timeout value %q: must be an integer", timeoutValue)
	}
	return timeout, nil
}

func currentMarker(active bool) string {
	if active {
		return "*"
	}
	return ""
}
