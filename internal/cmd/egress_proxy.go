// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dagucloud/dagu/v2/internal/runtime/egress"
	"github.com/spf13/cobra"
)

// EgressProxy returns the hidden command that runs the egress proxy of an agent
// worker (Spec 092, "Network"). It loads no Dagu configuration: the
// arguments are the whole configuration, and an unknown flag, a missing flag or
// a value that is not valid stops the command before it serves.
func EgressProxy() *cobra.Command {
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
	command.AddCommand(egressProxyServe(), egressProxyWait())
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

func egressProxyServe() *cobra.Command {
	command := &cobra.Command{
		Use:           "serve",
		Short:         "Serve the HTTP proxy for the allow list",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, _ []string) error {
			listen, err := requireFlag(cmd, "listen")
			if err != nil {
				return err
			}
			if err := checkHostPort("listen", listen); err != nil {
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
			upstream, err := readUpstream(cmd)
			if err != nil {
				return err
			}
			credentialRules, credential, err := readProxyCredential(cmd)
			if err != nil {
				return err
			}
			proxy, err := egress.New(egress.Options{
				Rules:           rules,
				Upstream:        upstream,
				CredentialRules: credentialRules,
				ProxyCredential: credential,
				Log: func(format string, args ...any) {
					_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
				},
			})
			if errors.Is(err, egress.ErrUpstreamNotAllowed) {
				return fmt.Errorf("flag --upstream: expected a host and port that an --allow or an --allow-with-credential entry lists: %w", err)
			}
			if errors.Is(err, egress.ErrCredentialRules) {
				return fmt.Errorf("flag --allow-with-credential: %w", err)
			}
			if err != nil {
				return err
			}
			listener, err := net.Listen("tcp", listen)
			if err != nil {
				return fmt.Errorf("flag --listen: cannot listen on %q: %w", listen, err)
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			go func() {
				<-ctx.Done()
				_ = listener.Close()
			}()
			_, _ = fmt.Fprintf(os.Stderr, "egress proxy: listening on %s with %d allowed destinations\n", listener.Addr(), rules.Len())
			if upstream != nil {
				_, _ = fmt.Fprintf(os.Stderr, "egress proxy: requests in origin form go to %s:%d\n", upstream.Host(), upstream.Port())
			}
			if credentialRules != nil {
				_, _ = fmt.Fprintf(os.Stderr, "egress proxy: %v need the proxy credential\n", credentialRules.List())
			}
			return proxy.Serve(listener)
		},
	}
	command.Flags().String("listen", "", "address to listen on, as host:port (required)")
	command.Flags().StringArray("allow", nil, "allowed destination, as host or host:port; repeat the flag (required)")
	command.Flags().String("upstream", "", "http or https URL that receives each request in origin form; its host and port must match an --allow entry")
	command.Flags().String("upstream-auth", string(egress.AuthXAPIKey), "form of the key for the upstream: x-api-key, bearer or anthropic-oauth")
	command.Flags().String("upstream-key-env", "", "name of the environment variable that holds the key of the upstream")
	command.Flags().String("upstream-key-file", "", "path of the file that holds the key of the upstream")
	command.Flags().StringArray("allow-with-credential", nil, "destination, as host or host:port, for a client that sends the proxy credential; repeat the flag")
	command.Flags().String("proxy-credential-env", "", "name of the environment variable that holds the proxy credential")
	command.Flags().String("proxy-credential-file", "", "path of the file that holds the proxy credential")
	return command
}

// maxUpstreamKeyBytes bounds the size of the key file.
const maxUpstreamKeyBytes = 16 * 1024

// readUpstream reads the options of the upstream. It returns nil when the command
// has no --upstream. It reads the key one time. No error holds the key.
func readUpstream(cmd *cobra.Command) (*egress.Upstream, error) {
	flags := cmd.Flags()
	rawURL, err := flags.GetString("upstream")
	if err != nil {
		return nil, err
	}
	if !flags.Changed("upstream") {
		for _, name := range []string{"upstream-auth", "upstream-key-env", "upstream-key-file"} {
			if flags.Changed(name) {
				value, _ := flags.GetString(name)
				return nil, fmt.Errorf("flag --%s: needs the flag --upstream, got %q", name, value)
			}
		}
		return nil, nil
	}
	authText, err := flags.GetString("upstream-auth")
	if err != nil {
		return nil, err
	}
	auth, err := egress.ParseUpstreamAuth(authText)
	if err != nil {
		return nil, fmt.Errorf("flag --upstream-auth: %w", err)
	}
	key, keyFlag, err := readSecretOption(cmd, "upstream", "upstream-key-env", "upstream-key-file")
	if err != nil {
		return nil, err
	}
	if err := egress.ValidateUpstreamKey(key); err != nil {
		return nil, fmt.Errorf("flag --%s: %w", keyFlag, err)
	}
	upstream, err := egress.NewUpstream(rawURL, auth, key)
	if err != nil {
		return nil, fmt.Errorf("flag --upstream: %w", err)
	}
	return upstream, nil
}

// readProxyCredential reads the destinations that need the proxy credential and
// the credential. It returns nil and "" when the command has no
// --allow-with-credential. New checks the rest: the length of the credential and
// the overlap with --allow. No error holds the credential.
func readProxyCredential(cmd *cobra.Command) (*egress.Rules, string, error) {
	flags := cmd.Flags()
	if !flags.Changed("allow-with-credential") {
		for _, name := range []string{"proxy-credential-env", "proxy-credential-file"} {
			if flags.Changed(name) {
				value, _ := flags.GetString(name)
				return nil, "", fmt.Errorf("flag --%s: needs the flag --allow-with-credential, got %q", name, value)
			}
		}
		return nil, "", nil
	}
	list, err := flags.GetStringArray("allow-with-credential")
	if err != nil {
		return nil, "", err
	}
	rules, err := egress.ParseRules(list)
	if err != nil {
		return nil, "", fmt.Errorf("flag --allow-with-credential: %w", err)
	}
	credential, _, err := readSecretOption(cmd, "allow-with-credential", "proxy-credential-env", "proxy-credential-file")
	if err != nil {
		return nil, "", err
	}
	return rules, credential, nil
}

// readSecretOption reads a secret from the one source that the command names:
// the variable of envFlag or the file of fileFlag. owner is the flag that needs
// the secret. It returns the secret and the name of the flag that named the
// source.
func readSecretOption(cmd *cobra.Command, owner, envFlag, fileFlag string) (value, option string, err error) {
	flags := cmd.Flags()
	envSet, fileSet := flags.Changed(envFlag), flags.Changed(fileFlag)
	switch {
	case envSet && fileSet:
		return "", "", fmt.Errorf("flag --%s and flag --%s: give exactly one, got both", envFlag, fileFlag)
	case !envSet && !fileSet:
		return "", "", fmt.Errorf("flag --%s: needs exactly one of --%s and --%s, got none", owner, envFlag, fileFlag)
	case envSet:
		name, err := flags.GetString(envFlag)
		if err != nil {
			return "", "", err
		}
		if name == "" {
			return "", "", fmt.Errorf(`flag --%s: expected the name of a variable, got ""`, envFlag)
		}
		value, ok := os.LookupEnv(name)
		if !ok {
			return "", "", fmt.Errorf("flag --%s: the variable %q does not exist", envFlag, name)
		}
		if value == "" {
			return "", "", fmt.Errorf("flag --%s: the variable %q is empty", envFlag, name)
		}
		return value, envFlag, nil
	}
	path, err := flags.GetString(fileFlag)
	if err != nil {
		return "", "", err
	}
	if path == "" {
		return "", "", fmt.Errorf(`flag --%s: expected the path of a file, got ""`, fileFlag)
	}
	data, err := readKeyFile(path)
	if err != nil {
		return "", "", fmt.Errorf("flag --%s: %w", fileFlag, err)
	}
	text := string(data)
	// The file ends with one line end: remove it, and only it.
	if rest, ok := strings.CutSuffix(text, "\n"); ok {
		text = strings.TrimSuffix(rest, "\r")
	}
	if text == "" {
		return "", "", fmt.Errorf("flag --%s: the file %q is empty", fileFlag, path)
	}
	return text, fileFlag, nil
}

// readKeyFile reads at most maxUpstreamKeyBytes. The error names the path and
// never holds the content.
func readKeyFile(path string) ([]byte, error) {
	file, err := os.Open(path) // #nosec G304 -- the operator names the path of the key.
	if err != nil {
		return nil, fmt.Errorf("cannot read %q: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxUpstreamKeyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("cannot read %q: %w", path, err)
	}
	if len(data) > maxUpstreamKeyBytes {
		return nil, fmt.Errorf("the file %q is larger than %d bytes", path, maxUpstreamKeyBytes)
	}
	return data, nil
}

func egressProxyWait() *cobra.Command {
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
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			for {
				dialer := net.Dialer{Timeout: time.Second}
				conn, dialErr := dialer.DialContext(ctx, "tcp", addr)
				if dialErr == nil {
					_ = conn.Close()
					return nil
				}
				select {
				case <-ctx.Done():
					return errors.Join(fmt.Errorf("%s does not accept connections after %s", addr, timeout), dialErr)
				case <-time.After(50 * time.Millisecond):
				}
			}
		},
	}
	command.Flags().String("addr", "", "address to wait for, as host:port (required)")
	command.Flags().Duration("timeout", 20*time.Second, "time to wait")
	return command
}
