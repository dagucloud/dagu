// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/dagucloud/dagu/v2/internal/runtime/egress"
	"github.com/spf13/cobra"
)

// EgressProxy returns the hidden command that runs the egress proxy of an agent
// worker. It loads no Dagu configuration: the arguments are the whole
// configuration, and an unknown flag, a missing flag or a value that is not
// valid stops the command before it serves.
func EgressProxy() *cobra.Command {
	dialer := &net.Dialer{Timeout: time.Second}
	return egressProxy(net.Listen, dialer.DialContext)
}

// listenFunc opens the listener of an address, as net.Listen does.
type listenFunc func(network, address string) (net.Listener, error)

// egressProxy builds the command on the function that opens the listener of
// serve and the function that makes each connection attempt of wait.
func egressProxy(listen listenFunc, dial egress.DialFunc) *cobra.Command {
	command := &cobra.Command{
		Use:           "egress-proxy",
		Short:         "Run the egress proxy of an agent worker",
		Hidden:        true,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	command.AddCommand(egressProxyServe(listen), egressProxyWait(dial))
	return command
}

// checkHostPort checks that the value has the form host:port with a port from 1 to
// 65535. The error names the flag, the expected form and the value.
func checkHostPort(flag, value string) error {
	host, port, err := net.SplitHostPort(value)
	if err != nil || host == "" {
		return fmt.Errorf("flag --%s: expected host:port, got %q", flag, value)
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return fmt.Errorf("flag --%s: expected a port from 1 to 65535, got %q", flag, value)
	}
	return nil
}

func requireFlag(cmd *cobra.Command, name string) (string, error) {
	value, err := cmd.Flags().GetString(name)
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", fmt.Errorf("flag --%s is required", name)
	}
	return value, nil
}

func egressProxyServe(listen listenFunc) *cobra.Command {
	command := &cobra.Command{
		Use:           "serve",
		Short:         "Serve the HTTP proxy for the allow list",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, _ []string) error {
			address, err := requireFlag(cmd, "listen")
			if err != nil {
				return err
			}
			if err := checkHostPort("listen", address); err != nil {
				return err
			}
			allow, err := cmd.Flags().GetStringArray("allow")
			if err != nil {
				return err
			}
			rules, err := egress.ParseRules(allow)
			if err != nil {
				return fmt.Errorf("flag --allow: %w", err)
			}
			maxConnections, err := cmd.Flags().GetInt("max-connections")
			if err != nil {
				return err
			}
			if maxConnections < 1 {
				return fmt.Errorf("flag --max-connections: expected a number above zero, got %d", maxConnections)
			}
			proxy, err := egress.New(egress.Options{
				Rules:          rules,
				MaxConnections: maxConnections,
				Log: func(format string, args ...any) {
					_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
				},
			})
			if err != nil {
				return err
			}
			listener, err := listen("tcp", address)
			if err != nil {
				return fmt.Errorf("flag --listen: cannot listen on %q: %w", address, err)
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			go func() {
				<-ctx.Done()
				_ = listener.Close()
			}()
			_, _ = fmt.Fprintf(os.Stderr, "egress proxy: listening on %s with %d allowed destinations\n", listener.Addr(), rules.Len())
			return proxy.Serve(listener)
		},
	}
	command.Flags().String("listen", "", "address to listen on, as host:port (required)")
	command.Flags().StringArray("allow", nil, "allowed destination, as host, host:port or [IPv6 address]:port; repeat the flag (required)")
	command.Flags().Int("max-connections", egress.DefaultMaxConnections, "most client connections open at once, tunnels included; the proxy closes a connection over it")
	return command
}

func egressProxyWait(dial egress.DialFunc) *cobra.Command {
	command := &cobra.Command{
		Use:           "wait",
		Short:         "Wait until an address accepts connections",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, _ []string) error {
			addr, err := requireFlag(cmd, "addr")
			if err != nil {
				return err
			}
			if err := checkHostPort("addr", addr); err != nil {
				return err
			}
			timeout, err := cmd.Flags().GetDuration("timeout")
			if err != nil {
				return err
			}
			if timeout <= 0 {
				return fmt.Errorf("flag --timeout: expected a duration above zero, got %s", timeout)
			}
			timedOut := fmt.Errorf("%s does not accept connections after %s", addr, timeout)
			ctx, cancel := context.WithTimeoutCause(cmd.Context(), timeout, timedOut)
			defer cancel()
			for {
				conn, dialErr := dial(ctx, "tcp", addr)
				if dialErr == nil {
					_ = conn.Close()
					return nil
				}
				select {
				case <-ctx.Done():
					if cause := context.Cause(ctx); !errors.Is(cause, timedOut) {
						return fmt.Errorf("wait for %s: %w", addr, cause)
					}
					return errors.Join(timedOut, dialErr)
				case <-time.After(50 * time.Millisecond):
				}
			}
		},
	}
	command.Flags().String("addr", "", "address to wait for, as host:port (required)")
	command.Flags().Duration("timeout", 20*time.Second, "time to wait")
	return command
}
