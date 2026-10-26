// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/dagucloud/dagu/v2/internal/browserhost/certproxy"
	"github.com/dagucloud/dagu/v2/internal/browserhost/writegate"
	cmnconfig "github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/hostpattern"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
)

const (
	// kindClientCertificate labels what the certificate proxy reports.
	kindClientCertificate = "client_certificate"
	// statusAsked marks a site that asked for a client certificate no entry
	// names.
	statusAsked          = "asked"
	stepCertificateLabel = "with.browser.client_certificates[%d]"
	hostCertificateLabel = "browser.client_certificates[%d] in the Dagu config"
)

// clientAuthError matches the network errors of a site that asks for a TLS
// client certificate the browser does not present, or refuses the one it
// presents.
var clientAuthError = regexp.MustCompile(`^ERR_(SSL_CLIENT_AUTH_[A-Z_]+|BAD_SSL_CLIENT_AUTH_CERT)$`)

// connectionError matches the network errors a TLS connection the proxy
// could not complete shows in the browser.
var connectionError = regexp.MustCompile(`^ERR_(SSL_[A-Z_]+|BAD_SSL_[A-Z_]+|CERT_[A-Z_]+|EMPTY_RESPONSE|CONNECTION_(CLOSED|RESET)|TUNNEL_CONNECTION_FAILED|PROXY_CONNECTION_FAILED)$`)

// urlHost returns the host of address, or "" when it names none.
func urlHost(address string) string {
	parsed, err := url.Parse(address)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

// loadClientCertificates loads the certificates a browser presents: the
// step's, then the host's, so the step's entry wins for a host both name.
// The step's relative files resolve against workDir. Warnings report
// certificates outside their validity period.
func loadClientCertificates(ctx context.Context, step []cmnconfig.BrowserClientCertificate, workDir string) ([]certproxy.Entry, []string, error) {
	var entries []certproxy.Entry
	var warnings []string
	now := time.Now()
	for _, group := range []struct {
		label        string
		certificates []cmnconfig.BrowserClientCertificate
		baseDir      string
	}{
		{stepCertificateLabel, step, workDir},
		{hostCertificateLabel, cmnconfig.GetConfig(ctx).Browser.ClientCertificates, ""},
	} {
		for i, certificate := range group.certificates {
			entry, entryWarnings, err := certproxy.Load(fmt.Sprintf(group.label, i), certificate, group.baseDir, now)
			if err != nil {
				return nil, nil, err
			}
			entries = append(entries, entry)
			warnings = append(warnings, entryWarnings...)
		}
	}
	return entries, warnings, nil
}

// loadTrustedCAs loads the host's trusted authorities.
func loadTrustedCAs(ctx context.Context) ([]certproxy.TrustedCA, error) {
	var entries []certproxy.TrustedCA
	for i, authority := range cmnconfig.GetConfig(ctx).Browser.TrustedCAs {
		entry, err := certproxy.LoadTrustedCA(fmt.Sprintf("browser.trusted_cas[%d]", i), authority)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// guardsCertificates reports whether a browser presenting certificates
// guards its other https: requests too: as the host config says, or
// whenever it presents certificates.
func guardsCertificates(ctx context.Context, certificates []certproxy.Entry) bool {
	if guard := cmnconfig.GetConfig(ctx).Browser.GuardClientCertificates; guard != nil {
		return *guard
	}
	return len(certificates) > 0
}

// withCertificatePassphrases returns secrets with the passphrases the step's
// certificates name, so they are masked and kept from the model like
// secrets even when written literally.
func withCertificatePassphrases(secrets map[string]string, certificates []cmnconfig.BrowserClientCertificate) map[string]string {
	for i, certificate := range certificates {
		if certificate.Passphrase == "" {
			continue
		}
		secrets = maps.Clone(secrets)
		if secrets == nil {
			secrets = map[string]string{}
		}
		secrets[fmt.Sprintf(stepCertificateLabel, i)+".passphrase"] = certificate.Passphrase
	}
	return secrets
}

// recordCertProxy stores in record where the certificate proxy runs, if the
// browser has one.
func recordCertProxy(record *browserhost.Record, proxy *certproxy.Handle) {
	if proxy == nil {
		return
	}
	record.CertProxyURL, record.CertProxyPID, record.CertProxyStartedAt = proxy.URL, proxy.PID, proxy.StartedAt
}

// recordWriteGate stores in record where the write gate runs, if the
// browser has one.
func recordWriteGate(record *browserhost.Record, gate *writegate.Handle) {
	if gate == nil {
		return
	}
	record.WriteGateURL, record.WriteGatePID, record.WriteGateStartedAt = gate.URL, gate.PID, gate.StartedAt
}

// recordedHandle locates the browser record describes.
func recordedHandle(record browserhost.Record) browserHandle {
	handle := browserHandle{
		CDPURL:           record.CDPURL,
		ExtensionID:      record.ExtensionID,
		ExtensionDir:     record.ExtensionDir,
		BrowserPID:       record.BrowserPID,
		BrowserStartedAt: record.BrowserStartedAt,
	}
	if record.CertProxyURL != "" {
		handle.CertProxy = &certproxy.Handle{URL: record.CertProxyURL, PID: record.CertProxyPID, StartedAt: record.CertProxyStartedAt}
	}
	if record.WriteGateURL != "" {
		handle.WriteGate = &writegate.Handle{URL: record.WriteGateURL, PID: record.WriteGatePID, StartedAt: record.WriteGateStartedAt}
	}
	return handle
}

// refusalPage matches what a web server, such as nginx or IIS, shows in
// place of a site when the browser presented no client certificate it
// accepts.
var refusalPage = regexp.MustCompile(`(?i)no required ssl certificate was sent|ssl certificate error|client certificate|403\.(7|13|16)\b`)

// certificateFailure explains what the browser shows when a client
// certificate kept the page from opening, using what the certificate proxy
// reported for the page's host, or returns nil when the page is unrelated
// to client certificates. presented maps the hosts the proxy presented a
// certificate to, to its label. Without a known host, a proxy failure
// explains only an error that a failed TLS connection causes.
func certificateFailure(page pageOutcome, failures []certproxy.Event, presented map[string]string, proxy bool) error {
	switch {
	case page.Code != "":
		if failure, ok := failureFor(page.Host, failures, connectionError.MatchString(page.Code)); ok {
			return describeFailure(failure, "the browser shows "+page.Code, proxy)
		}
		if clientAuthError.MatchString(page.Code) {
			return missingCertificate(page.Host, page.Code, proxy)
		}
	case page.Status >= http.StatusBadRequest:
		if failure, ok := failureFor(page.Host, failures, false); ok && refusesCertificate(failure) {
			return describeFailure(failure, fmt.Sprintf("the site answered %d", page.Status), proxy)
		}
		if !refusalPage.MatchString(page.Title + "\n" + page.Text) {
			return nil
		}
		if label, ok := presented[hostpattern.Canonical(page.Host)]; ok {
			return fmt.Errorf("%s rejected the client certificate %s: it answered %d %s", page.Host, label, page.Status, strings.TrimSpace(page.Title))
		}
		return missingCertificate(page.Host, fmt.Sprintf("it answered %d", page.Status), proxy)
	}
	return nil
}

// failureFor returns the last failure the proxy reported for host. Without
// a host, it returns the last failure only when unknown is set.
func failureFor(host string, failures []certproxy.Event, unknown bool) (certproxy.Event, bool) {
	for _, failure := range slices.Backward(failures) {
		if host != "" && hostpattern.Canonical(failure.Host) == hostpattern.Canonical(host) || host == "" && unknown {
			return failure, true
		}
	}
	return certproxy.Event{}, false
}

// refusesCertificate reports whether failure is a site refusing the browser
// for its client certificate.
func refusesCertificate(failure certproxy.Event) bool {
	return failure.Kind == certproxy.EventClientCertificateMissing || failure.Kind == certproxy.EventClientCertificateRejected
}

// describeFailure states failure, with detail on what the browser showed.
func describeFailure(failure certproxy.Event, detail string, proxy bool) error {
	if failure.Kind == certproxy.EventClientCertificateMissing {
		return missingCertificate(failure.Host, detail, proxy)
	}
	return fmt.Errorf("%s (%s)", failure.Message, detail)
}

// missingCertificate states that host asks for a client certificate no
// entry names, and how to name one.
func missingCertificate(host, detail string, proxy bool) error {
	subject := cmp.Or(host, "the site")
	message := fmt.Sprintf("%s asks for a TLS client certificate, and none is configured for it (%s); "+
		"set with.browser.client_certificates or browser.client_certificates in the Dagu config", subject, detail)
	if proxy && clientAuthError.MatchString(detail) {
		// The proxy answers such a request from a host a certificate
		// names; the browser met it on a route that bypassed the proxy.
		message += "; if one is configured for it, a Chrome enterprise policy may be setting the browser's proxy"
	}
	return errors.New(message)
}

// certificateTrouble reports whether event is about a client certificate
// itself, rather than about reaching a host, so it belongs in a failed
// step's summary whichever host failed.
func certificateTrouble(event certproxy.Event) bool {
	switch event.Kind {
	case certproxy.EventClientCertificateRejected, certproxy.EventClientCertificateMissing,
		certproxy.EventServerCertificate, certproxy.EventProxyAuth, certproxy.EventGuardOff:
		return true
	}
	return false
}

// describeCertificateEvent writes what the certificate proxy reported in one
// line.
func describeCertificateEvent(event certproxy.Event) string {
	if event.Message != "" {
		return event.Message
	}
	return strings.TrimSpace(event.Kind + " " + event.Host)
}

// logCertificateEvent records what the certificate proxy reported while the
// operation at index ran. A site asking for a certificate is not a failure
// by itself: one that opens without it can ask.
func logCertificateEvent(t *agentstep.Timeline, index int, event certproxy.Event) {
	status := agentstep.StatusFailed
	switch event.Kind {
	case certproxy.EventConnect:
		status = agentstep.StatusCompleted
	case certproxy.EventClientCertificateMissing:
		status = statusAsked
	}
	content := t.Masker.MaskString(describeCertificateEvent(event))
	_, _ = fmt.Fprintf(t.Log, "%s %s %s %s\n", t.Position(index), kindClientCertificate, status, content)
	t.AppendEvent(ir.AgentSessionEvent{Type: agentstep.EventOperation, Name: kindClientCertificate, Status: status, Content: content})
}
