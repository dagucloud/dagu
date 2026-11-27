// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// logRecord is one logged line: its level and message.
type logRecord struct {
	Level string `json:"level"`
	Msg   string `json:"msg"`
}

const (
	ignoredClientCA = "Peer TLS setting ignored: peer.client_ca_file (/etc/dagu/ca.crt) has no effect while peer.insecure is not false; set peer.insecure: false to use it"
	ignoredCert     = "Peer TLS setting ignored: peer.cert_file (/etc/dagu/client.crt) has no effect while peer.insecure is not false; set peer.insecure: false to use it"
	ignoredSkip     = "Peer TLS setting ignored: peer.skip_tls_verify (true) has no effect while peer.insecure is not false; set peer.insecure: false to use it"
)

func TestLogPeerTransport(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command string
		cfg     *config.Config
		want    []logRecord
	}{
		{
			name:    "Unset",
			command: "worker",
			cfg:     peerConfig(config.SourceKindDefault, config.Peer{Insecure: true}),
			want:    []logRecord{{"INFO", "Peer transport: h2c (from no TLS source)"}},
		},
		{
			name:    "UnsetIgnoresFile",
			command: "worker",
			cfg:     peerConfig(config.SourceKindDefault, config.Peer{Insecure: true, ClientCaFile: "/etc/dagu/ca.crt"}),
			want: []logRecord{
				{"WARN", ignoredClientCA},
				{"INFO", "Peer transport: h2c (from no TLS source)"},
			},
		},
		{
			name:    "InsecureTrue",
			command: "scheduler",
			cfg:     peerConfig(config.SourceKindFile, config.Peer{Insecure: true, CertFile: "/etc/dagu/client.crt", SkipTLSVerify: true}),
			want: []logRecord{
				{"WARN", ignoredCert},
				{"WARN", ignoredSkip},
				{"INFO", "Peer transport: h2c (from peer.insecure: true)"},
			},
		},
		{
			name:    "InsecureFalse",
			command: "start-all",
			cfg:     peerConfig(config.SourceKindEnv, config.Peer{CertFile: "/etc/dagu/client.crt", KeyFile: "/etc/dagu/client.key"}),
			want:    []logRecord{{"INFO", "Peer transport: TLS (from peer.insecure: false)"}},
		},
		{
			// The client fails to start with its own message.
			name:    "InsecureFalseWithoutTLS",
			command: "worker",
			cfg:     peerConfig(config.SourceKindFlag, config.Peer{}),
		},
		{
			name:    "CACertFile",
			command: "worker",
			cfg:     peerConfig(config.SourceKindDefault, config.Peer{Insecure: true, CACertFile: "/etc/dagu/ca.crt"}),
			want:    []logRecord{{"INFO", "Peer transport: built-in TLS (from peer.ca_cert_file)"}},
		},
		{
			name:    "InsecureFalseCACertFile",
			command: "worker",
			cfg:     peerConfig(config.SourceKindFile, config.Peer{CACertFile: "/etc/dagu/ca.crt"}),
			want:    []logRecord{{"INFO", "Peer transport: built-in TLS (from peer.ca_cert_file)"}},
		},
		{
			name:    "InsecureTrueIgnoresCACertFile",
			command: "worker",
			cfg:     peerConfig(config.SourceKindEnv, config.Peer{Insecure: true, CACertFile: "/etc/dagu/ca.crt"}),
			want: []logRecord{
				{"WARN", "Peer TLS setting ignored: peer.ca_cert_file (/etc/dagu/ca.crt) has no effect while peer.insecure is true; remove peer.insecure: true to use it"},
				{"INFO", "Peer transport: h2c (from peer.insecure: true)"},
			},
		},
		{
			name:    "PublishedCA",
			command: "scheduler",
			cfg:     peerConfig(config.SourceKindDefault, config.Peer{Insecure: true, PublishedCAFile: publishedCAFile(t)}),
			want:    []logRecord{{"INFO", "Peer transport: built-in TLS (from the coordinator's data directory)"}},
		},
		{
			name:    "CoordinatorOnlyServes",
			command: "coordinator",
			cfg:     peerConfig(config.SourceKindDefault, config.Peer{Insecure: true, CertFile: "/etc/dagu/client.crt"}),
		},
		{
			name:    "CLICommand",
			command: "status",
			cfg:     peerConfig(config.SourceKindDefault, config.Peer{Insecure: true, ClientCaFile: "/etc/dagu/ca.crt"}),
			want: []logRecord{
				{"DEBUG", ignoredClientCA},
				{"DEBUG", "Peer transport: h2c (from no TLS source)"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, logRecords(t, tc.command, tc.cfg))
		})
	}

	t.Run("CoordinatorDisabled", func(t *testing.T) {
		cfg := peerConfig(config.SourceKindDefault, config.Peer{Insecure: true})
		cfg.Coordinator.Enabled = false
		assert.Empty(t, logRecords(t, "server", cfg))
	})
}

// peerConfig returns a config with peer whose peer.insecure came from a
// source of kind.
func peerConfig(kind config.SourceKind, peer config.Peer) *config.Config {
	peer.InsecureUnset = kind == config.SourceKindDefault
	return &config.Config{
		Core:        config.Core{Peer: peer},
		Coordinator: config.Coordinator{Enabled: true},
		Sources:     map[string]config.ValueSource{"peer.insecure": {Kind: kind}},
	}
}

// publishedCAFile returns the CA certificate a coordinator publishes in a new
// data directory.
func publishedCAFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ca, err := coordinator.LoadOrCreatePeerCA(filepath.Join(dir, "peer-ca.pem"))
	require.NoError(t, err)
	path := filepath.Join(dir, "peer-ca.crt")
	require.NoError(t, ca.PublishCertificate(path))
	return path
}

// logRecords returns what logPeerTransport logs for command and cfg.
func logRecords(t *testing.T, command string, cfg *config.Config) []logRecord {
	t.Helper()
	return captureLogs(t, func(ctx context.Context) {
		logPeerTransport(ctx, command, cfg)
	})
}

// captureLogs returns what fn logs at level debug and above.
func captureLogs(t *testing.T, fn func(context.Context)) []logRecord {
	t.Helper()
	var out bytes.Buffer
	ctx := logger.WithLogger(t.Context(), logger.NewLogger(
		logger.WithDebug(), logger.WithQuiet(), logger.WithFormat("json"), logger.WithWriter(&out),
	))
	fn(ctx)

	var records []logRecord
	for line := range strings.Lines(out.String()) {
		var record logRecord
		require.NoError(t, json.Unmarshal([]byte(line), &record))
		records = append(records, record)
	}
	return records
}
