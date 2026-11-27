// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
)

// peerCertExpiryWarning is how close to its expiry a coordinator certificate
// gets a warning at start.
const peerCertExpiryWarning = 14 * 24 * time.Hour

// loadPeerKeyPair loads peer.cert_file and peer.key_file. Its errors name the
// file to fix.
func loadPeerKeyPair(peer config.Peer) (tls.Certificate, error) {
	certPEM, err := fileutil.ReadFile(peer.CertFile)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("read peer.cert_file: %w", err)
	}
	keyPEM, err := fileutil.ReadFile(peer.KeyFile)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("read peer.key_file: %w", err)
	}
	if _, err := parseLeafCertificate(certPEM); err != nil {
		return tls.Certificate{}, fmt.Errorf("peer.cert_file %s holds no valid certificate: %w", peer.CertFile, err)
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf(
			"peer.key_file %s is not the key of the certificate in peer.cert_file %s (%v): set peer.key_file to the private key of that certificate",
			peer.KeyFile, peer.CertFile, err)
	}
	return pair, nil
}

// parseLeafCertificate parses the first certificate in certPEM.
func parseLeafCertificate(certPEM []byte) (*x509.Certificate, error) {
	for block, rest := pem.Decode(certPEM); block != nil; block, rest = pem.Decode(rest) {
		if block.Type == "CERTIFICATE" {
			return x509.ParseCertificate(block.Bytes)
		}
	}
	return nil, errors.New("no PEM certificate block")
}

// warnCoordinatorCertificate warns when clients that dial advertise would
// fail to verify leaf, now or within peerCertExpiryWarning.
func warnCoordinatorCertificate(ctx context.Context, certFile string, leaf *x509.Certificate, advertise string) {
	if err := leaf.VerifyHostname(advertise); err != nil {
		logger.Warn(ctx, "Coordinator certificate does not name the advertised address",
			tag.Cert(certFile),
			tag.AdvertiseAddress(advertise),
			tag.Reason("clients that find the coordinator by this address fail to verify it; add the address to the certificate, or set coordinator.advertise to a name it covers"),
		)
	}
	expiry := []slog.Attr{
		tag.Cert(certFile),
		slog.Time("expires-at", leaf.NotAfter),
		tag.Reason("clients refuse an expired certificate; replace it with a renewed one"),
	}
	switch now := time.Now(); {
	case now.After(leaf.NotAfter):
		logger.Warn(ctx, "Coordinator certificate has expired", expiry...)
	case leaf.NotAfter.Sub(now) < peerCertExpiryWarning:
		logger.Warn(ctx, "Coordinator certificate expires within 14 days", expiry...)
	}
}

// warnPeerClientAuth warns when a process that serves the coordinator also
// dials it with peer.cert_file under mTLS, and the coordinator would refuse
// that certificate as a client certificate.
func warnPeerClientAuth(ctx context.Context, peer config.Peer) {
	if peer.Insecure || peer.ClientCaFile == "" || peer.CertFile == "" || peer.KeyFile == "" {
		return
	}
	// The coordinator has loaded this file, so the read or the parse fails
	// only when the file changed since.
	certPEM, err := fileutil.ReadFile(peer.CertFile)
	if err != nil {
		return
	}
	leaf, err := parseLeafCertificate(certPEM)
	if err != nil || allowsClientAuth(leaf) {
		return
	}
	logger.Warn(ctx, "Peer certificate does not allow client authentication",
		tag.Cert(peer.CertFile),
		tag.Reason("this process presents it to its own coordinator, which peer.client_ca_file makes require client certificates; local runs work, but dispatching runs to workers fails until the certificate allows client authentication"),
	)
}

// allowsClientAuth reports whether a TLS server accepts cert as a client
// certificate. A certificate without extended key usages allows any usage.
func allowsClientAuth(cert *x509.Certificate) bool {
	if len(cert.ExtKeyUsage) == 0 && len(cert.UnknownExtKeyUsage) == 0 {
		return true
	}
	return slices.ContainsFunc(cert.ExtKeyUsage, func(usage x509.ExtKeyUsage) bool {
		return usage == x509.ExtKeyUsageClientAuth || usage == x509.ExtKeyUsageAny
	})
}
