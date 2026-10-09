// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dagucloud/dagu/v2/internal/license"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/spf13/cobra"
)

// License returns the parent command for license management.
func License() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "license",
		Short: "Manage Dagu license",
		Long:  "Activate, deactivate, and check Dagu license status.",
	}

	cmd.AddCommand(licenseActivate())
	cmd.AddCommand(licenseDeactivate())
	cmd.AddCommand(licenseCheck())

	return cmd
}

func licenseActivate() *cobra.Command {
	return NewCommand(
		&cobra.Command{
			Use:   "activate [key]",
			Short: "Activate this server's license",
			Long: `Activate this server's Dagu license.

With a key, activate the server key or license key from Dagu Console.

Without a key, request a license from Dagu Console: open the printed URL in
any browser and approve the request as a workspace owner. The license is
saved once approved; restart a running Dagu server to load it. Without a
terminal, a key is required.`,
			Args: cobra.MaximumNArgs(1),
		}, nil, func(ctx *Context, args []string) error {
			if len(args) == 1 {
				return activateWithKey(ctx, args[0])
			}
			// Waiting for a browser approval would stall a script that lost its key.
			if !isTerminal(os.Stdin) {
				return errors.New("a license key is required when not running in a terminal: dagu license activate <key>")
			}
			return activateFromConsole(ctx)
		},
	)
}

func activateWithKey(ctx *Context, key string) error {
	mgr, err := newLicenseManager(ctx, key)
	if err != nil {
		return err
	}

	result, err := mgr.ActivateWithKey(ctx, key)
	if err != nil {
		return fmt.Errorf("activation failed: %w", err)
	}
	defer mgr.Stop()

	fmt.Printf("License activated successfully!\n")
	fmt.Printf("  Plan:     %s\n", result.Plan)
	fmt.Printf("  Features: %s\n", strings.Join(result.Features, ", "))
	if !result.Expiry.IsZero() {
		fmt.Printf("  Expires:  %s\n", result.Expiry.Format("2006-01-02"))
	}

	return nil
}

func licenseDeactivate() *cobra.Command {
	return NewCommand(
		&cobra.Command{
			Use:   "deactivate",
			Short: "Remove the local license activation",
		}, nil, func(ctx *Context, _ []string) error {
			mgr, err := newLicenseManager(ctx, ctx.Config.License.Key)
			if err != nil {
				return err
			}

			if err := mgr.Start(ctx); err != nil {
				return err
			}
			defer mgr.Stop()

			consoleURL := mgr.Status().ConsoleURL
			result, err := mgr.Deactivate(ctx)
			if err != nil {
				return fmt.Errorf("deactivation failed: %w", err)
			}

			fmt.Println("License deactivated. Running in community mode.")
			if result.ReleaseFailed && consoleURL != "" {
				fmt.Printf("Dagu Console could not be reached; this server still uses a slot until you disconnect it at %s\n", consoleURL)
			}
			return nil
		},
	)
}

func licenseCheck() *cobra.Command {
	return NewCommand(
		&cobra.Command{
			Use:   "check",
			Short: "Display current license status",
		}, nil, func(ctx *Context, _ []string) error {
			mgr, err := newLicenseManager(ctx, ctx.Config.License.Key)
			if err != nil {
				return err
			}

			if err := mgr.Start(ctx); err != nil {
				return err
			}
			defer mgr.Stop()

			printLicenseStatus(os.Stdout, mgr.Status())
			return nil
		},
	)
}

// activateFromConsole requests a license from Dagu Console and waits until a
// workspace owner approves this server in a browser.
func activateFromConsole(ctx *Context) error {
	mgr, err := newLicenseManager(ctx, ctx.Config.License.Key)
	if err != nil {
		return err
	}
	if err := mgr.Start(ctx); err != nil {
		return err
	}
	defer mgr.Stop()

	status, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("activation failed: %w", err)
	}
	fmt.Printf("Open this URL in a browser and approve the request:\n\n  %s\n\n", status.URL)
	fmt.Printf("Confirm that Dagu Console shows the code %s. The request expires at %s.\n",
		status.Code, status.ExpiresAt.Format(time.Kitchen))
	fmt.Println("Waiting for approval... (press Ctrl-C to cancel)")

	waitCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-waitCtx.Done():
			mgr.CancelConnect()
			return errors.New("activation cancelled")
		case <-ticker.C:
		}

		status := mgr.ConnectStatus()
		switch status.State {
		case license.ConnectPending:
			continue
		case license.ConnectGranted:
			fmt.Println("License activated from Dagu Console. Restart a running Dagu server to load it.")
			printLicenseStatus(os.Stdout, mgr.Status())
			return nil
		case license.ConnectIdle, license.ConnectFailed, license.ConnectExpired:
			reason := status.Error
			if reason == "" {
				reason = "the request to Dagu Console ended"
			}
			return fmt.Errorf("activation failed: %s", reason)
		}
	}
}

// printLicenseStatus writes the license summary shown by `license check`.
func printLicenseStatus(w io.Writer, st license.Status) {
	if st.Community {
		_, _ = fmt.Fprintln(w, "License: Community mode (no license)")
		if st.Failure != "" {
			_, _ = fmt.Fprintf(w, "  Problem:      %s\n", st.Failure)
		}
		return
	}

	_, _ = fmt.Fprintf(w, "License Status\n")
	_, _ = fmt.Fprintf(w, "  Plan:         %s\n", st.Plan)
	_, _ = fmt.Fprintf(w, "  Features:     %s\n", strings.Join(st.Features, ", "))
	if !st.Expiry.IsZero() {
		_, _ = fmt.Fprintf(w, "  Expires:      %s\n", st.Expiry.Format("2006-01-02"))
	}
	if st.GracePeriod {
		_, _ = fmt.Fprintf(w, "  Status:       EXPIRED (grace period active)\n")
	} else {
		_, _ = fmt.Fprintf(w, "  Status:       Active\n")
	}

	rows := []struct{ label, value string }{
		{"Workspace", st.Workspace},
		{"Server", st.ServerName},
		{"Server ID", st.ServerID},
		{"License ID", st.LicenseID},
		{"Connected", connectedViaLabel(st.ConnectedVia)},
		{"Checked in", formatCheckIn(st.LastCheckIn)},
		{"Manage", st.ConsoleURL},
		{"Problem", st.Failure},
	}
	for _, row := range rows {
		if row.value != "" {
			_, _ = fmt.Fprintf(w, "  %-14s%s\n", row.label+":", row.value)
		}
	}
}

func connectedViaLabel(via string) string {
	switch via {
	case license.ConnectedViaConsole:
		return "via Dagu Console"
	case license.ConnectedViaKey:
		return "with a license key"
	case license.ConnectedViaEnv:
		return "by environment variable"
	case license.ConnectedViaConfig:
		return "by license.key in the config file"
	case license.ConnectedViaFile:
		return "by license file"
	}
	return via
}

func formatCheckIn(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

func newLicenseManager(ctx *Context, configKey string) (*license.Manager, error) {
	pubKey, err := license.PublicKey()
	if err != nil {
		return nil, fmt.Errorf("failed to load license public key: %w", err)
	}

	licenseDir := file.LicenseDir(ctx.Config)
	store := file.NewLicenseStore(ctx, ctx.backend.Collection(persis.CollectionLicense))
	return license.NewManager(license.ManagerConfig{
		LicenseDir: licenseDir,
		ConfigKey:  configKey,
		CloudURL:   ctx.Config.License.CloudURL,
		ServerName: ctx.Config.License.ServerName,
	}, pubKey, store, slog.Default()), nil
}
