// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
)

// peerInsecureKey is the setting that selects h2c or the TLS settings.
const peerInsecureKey = "peer.insecure"

// peerSetting is a TLS setting of the peer client and its value.
type peerSetting struct {
	key   string
	value string
}

// logPeerTransport logs, at start, each TLS setting that the transport
// command uses to call the coordinator ignores, then that transport.
// Services that call the coordinator log a warning per setting and the
// transport at level info. Other commands log both at level debug, so their
// terminal output does not change.
func logPeerTransport(ctx context.Context, command string, cfg *config.Config) {
	logIgnored, logLine := logger.Debug, logger.Debug
	switch command {
	case "coordinator":
		// It only serves, and the TLS settings apply to serving.
		return
	case "scheduler", "server", "start-all":
		if !cfg.Coordinator.Enabled {
			return
		}
		logIgnored, logLine = logger.Warn, logger.Info
	case "worker":
		logIgnored, logLine = logger.Warn, logger.Info
	}
	line, ignored := peerTransport(cfg)
	if line == "" {
		return
	}
	for _, setting := range ignored {
		logIgnored(ctx, fmt.Sprintf(
			"Peer TLS setting ignored: %s (%s) has no effect while %s is not false; set %s: false to use it",
			setting.key, setting.value, peerInsecureKey, peerInsecureKey,
		))
	}
	logLine(ctx, line)
}

// peerTransport returns the start line for the peer client transport and the
// TLS settings that it ignores. The line is empty when peer.insecure is false
// without a TLS setting, as the client then fails to start.
func peerTransport(cfg *config.Config) (string, []peerSetting) {
	peer := cfg.Core.Peer
	settings := peerTLSSettings(peer)
	if !peer.Insecure {
		if len(settings) == 0 {
			return "", nil
		}
		return "Peer transport: TLS (from " + peerInsecureKey + ": false)", nil
	}
	if cfg.Sources[peerInsecureKey].Kind == config.SourceKindDefault {
		return "Peer transport: h2c (from no TLS source)", settings
	}
	return "Peer transport: h2c (from " + peerInsecureKey + ": true)", settings
}

// peerTLSSettings returns the TLS settings that peer sets, in config order.
func peerTLSSettings(peer config.Peer) []peerSetting {
	var settings []peerSetting
	for _, setting := range []peerSetting{
		{"peer.cert_file", peer.CertFile},
		{"peer.key_file", peer.KeyFile},
		{"peer.client_ca_file", peer.ClientCaFile},
	} {
		if setting.value != "" {
			settings = append(settings, setting)
		}
	}
	if peer.SkipTLSVerify {
		settings = append(settings, peerSetting{"peer.skip_tls_verify", "true"})
	}
	return settings
}
