// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/dagucloud/dagu/internal/license"
	"github.com/dagucloud/dagu/internal/persis/file"
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
			Use:   "activate <key>",
			Short: "Activate a license key",
			Args:  cobra.ExactArgs(1),
		}, nil, func(ctx *Context, args []string) error {
			key := args[0]

			pubKey, err := license.PublicKey()
			if err != nil {
				return fmt.Errorf("failed to load license public key: %w", err)
			}

			licenseDir := file.LicenseDir(ctx.Config)
			store := file.NewLicenseStore(ctx.Config)

			mgr := license.NewManager(license.ManagerConfig{
				LicenseDir: licenseDir,
				ConfigKey:  key,
				CloudURL:   ctx.Config.License.CloudURL,
			}, pubKey, store, slog.Default())

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
		},
	)
}

func licenseDeactivate() *cobra.Command {
	return NewCommand(
		&cobra.Command{
			Use:   "deactivate",
			Short: "Remove the local license activation",
		}, nil, func(ctx *Context, _ []string) error {
			pubKey, err := license.PublicKey()
			if err != nil {
				return fmt.Errorf("failed to load license public key: %w", err)
			}

			licenseDir := file.LicenseDir(ctx.Config)
			store := file.NewLicenseStore(ctx.Config)

			mgr := license.NewManager(license.ManagerConfig{
				LicenseDir: licenseDir,
				ConfigKey:  ctx.Config.License.Key,
				CloudURL:   ctx.Config.License.CloudURL,
			}, pubKey, store, slog.Default())

			if err := mgr.Start(ctx); err != nil {
				return err
			}
			defer mgr.Stop()

			if err := mgr.Deactivate(ctx); err != nil {
				return fmt.Errorf("deactivation failed: %w", err)
			}

			fmt.Println("License deactivated. Running in community mode.")
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
			pubKey, err := license.PublicKey()
			if err != nil {
				return fmt.Errorf("failed to load license public key: %w", err)
			}

			licenseDir := file.LicenseDir(ctx.Config)
			store := file.NewLicenseStore(ctx.Config)

			mgr := license.NewManager(license.ManagerConfig{
				LicenseDir: licenseDir,
				ConfigKey:  ctx.Config.License.Key,
				CloudURL:   ctx.Config.License.CloudURL,
			}, pubKey, store, slog.Default())

			if err := mgr.Start(ctx); err != nil {
				return err
			}
			defer mgr.Stop()

			checker := mgr.Checker()
			if checker.IsCommunity() {
				fmt.Println("License: Community mode (no license)")
				return nil
			}

			fmt.Printf("License Status\n")
			fmt.Printf("  Plan:         %s\n", checker.Plan())
			if claims := checker.Claims(); claims != nil {
				fmt.Printf("  Features:     %s\n", strings.Join(claims.Features, ", "))
				if claims.ExpiresAt != nil {
					fmt.Printf("  Expires:      %s\n", claims.ExpiresAt.Format("2006-01-02"))
				}
			}
			if checker.IsGracePeriod() {
				fmt.Printf("  Status:       EXPIRED (grace period active)\n")
			} else {
				fmt.Printf("  Status:       Active\n")
			}

			return nil
		},
	)
}
