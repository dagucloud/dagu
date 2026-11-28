// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
)

const (
	// peerInsecureKey is the setting that selects h2c or the TLS settings.
	peerInsecureKey = "peer.insecure"
	// peerCACertFileKey is the CA certificate that selects built-in TLS.
	peerCACertFileKey = "peer.ca_cert_file"
	// startLineFormat is the start line, from the transport and what
	// selected it.
	startLineFormat = "Peer transport: %s"
)

// peerSetting is a TLS setting of the peer client and its value.
type peerSetting struct {
	key   string
	value string
}

// logPeerTransport logs, at start, the transport that command uses to call
// the coordinator, after one warning per TLS setting that transport ignores.
// Services that call the coordinator log the warnings at level warn and the
// transport at level info. Other commands log both at level debug, so their
// terminal output does not change.
func logPeerTransport(ctx context.Context, command string, cfg *config.Config) {
	logClientTransport(ctx, command, cfg, coordinator.ConfigFromPeer(cfg.Core.Peer).StartTransport)
}

// logClientTransport is logPeerTransport for a command whose client starts
// with the transport that start returns, such as a worker with a worker
// certificate.
func logClientTransport(ctx context.Context, command string, cfg *config.Config, start func() (string, error)) {
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
	line, ignored := peerTransport(cfg, start)
	if line == "" {
		return
	}
	for _, setting := range ignored {
		logIgnored(ctx, ignoredSettingWarning(setting))
	}
	logLine(ctx, line)
}

// ignoredSettingWarning is the warning for a TLS setting that the transport
// ignores, with the change that would use it.
func ignoredSettingWarning(setting peerSetting) string {
	if setting.key == peerCACertFileKey {
		return fmt.Sprintf(
			"Peer TLS setting ignored: %s (%s) has no effect while %s is true; remove %s: true to use it",
			setting.key, setting.value, peerInsecureKey, peerInsecureKey,
		)
	}
	return fmt.Sprintf(
		"Peer TLS setting ignored: %s (%s) has no effect while %s is not false; set %s: false to use it",
		setting.key, setting.value, peerInsecureKey, peerInsecureKey,
	)
}

// peerTransport returns the start line for the transport that start
// returns, and the TLS settings that it ignores. The line is empty when
// peer.insecure is false without a TLS setting or a TLS source, as the
// client then fails to start.
func peerTransport(cfg *config.Config, start func() (string, error)) (string, []peerSetting) {
	peer := cfg.Core.Peer
	transport, err := start()
	if err != nil {
		return "", nil
	}
	line := fmt.Sprintf(startLineFormat, transport)
	if !peer.Insecure {
		return line, nil
	}
	ignored := peerTLSSettings(peer)
	if !peer.InsecureUnset && peer.CACertFile != "" {
		ignored = append(ignored, peerSetting{peerCACertFileKey, peer.CACertFile})
	}
	return line, ignored
}

// peerTLSSettings returns the settings of TLS with peer.insecure: false that
// peer sets, in config order.
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
