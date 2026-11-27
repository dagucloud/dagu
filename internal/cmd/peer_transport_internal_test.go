// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
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
	return &config.Config{
		Core:        config.Core{Peer: peer},
		Coordinator: config.Coordinator{Enabled: true},
		Sources:     map[string]config.ValueSource{"peer.insecure": {Kind: kind}},
	}
}

// logRecords returns what logPeerTransport logs for command and cfg at
// level debug and above.
func logRecords(t *testing.T, command string, cfg *config.Config) []logRecord {
	t.Helper()
	var out bytes.Buffer
	ctx := logger.WithLogger(t.Context(), logger.NewLogger(
		logger.WithDebug(), logger.WithQuiet(), logger.WithFormat("json"), logger.WithWriter(&out),
	))
	logPeerTransport(ctx, command, cfg)

	var records []logRecord
	for line := range strings.Lines(out.String()) {
		var record logRecord
		require.NoError(t, json.Unmarshal([]byte(line), &record))
		records = append(records, record)
	}
	return records
}
