// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/browserhost/certproxy"
	cmnconfig "github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"software.sslmate.com/src/go-pkcs12"
)

// writeClientCertificate writes a PKCS#12 file holding a self-signed client
// certificate for commonName, protected by passphrase.
func writeClientCertificate(t *testing.T, commonName, passphrase string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	data, err := pkcs12.Modern.Encode(key, cert, nil, passphrase)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), commonName+".p12")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

func TestTrustedCAsReachBrowser(t *testing.T) {
	t.Parallel()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, key.Public(), key)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	for _, host := range []string{"portal.example.com", "10.0.0.1", "localhost", "127.0.0.1", "::1", "169.254.1.1", "*.localhost"} {
		t.Run(host, func(t *testing.T) {
			run := newTestRun(t, pageModel(nil))
			run.hostTrustedCAs = []cmnconfig.BrowserTrustedCA{{Hosts: []string{host}, CAFile: path}}
			result := run.execute(`{"do":[{"goto":"https://portal.example.com/"}]}`, nil)
			require.NoError(t, result.err)
			require.Len(t, run.launcher.launches, 1)
			require.Len(t, run.launcher.launches[0].TrustedCAs, 1)
			assert.Equal(t, []string{host}, run.launcher.launches[0].TrustedCAs[0].Hosts)
		})
	}
	run := newTestRun(t, pageModel(nil))
	run.hostTrustedCAs = []cmnconfig.BrowserTrustedCA{{Hosts: []string{"portal.example.com"}, CAFile: filepath.Join(t.TempDir(), "missing.pem")}}
	require.ErrorContains(t, run.execute(`{"do":[{"goto":"https://portal.example.com/"}]}`, nil).err, "browser.trusted_cas[0]")
	assert.Empty(t, run.launcher.launches)
}

// The step's certificates come before the host's, so the step's entry wins
// for a host both name.
func TestClientCertificatesReachLaunch(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	run.hostCertificates = []cmnconfig.BrowserClientCertificate{
		{Hosts: []string{"*"}, PFXFile: writeClientCertificate(t, "device", "")},
	}
	steps := fmt.Sprintf(`{
		"browser": {"client_certificates": [{"hosts": ["portal.example.com"], "pfx_file": %q, "passphrase": "s3cret-phrase"}]},
		"do": [{"act": "Click the checkout button"}]
	}`, writeClientCertificate(t, "portal", "s3cret-phrase"))
	require.NoError(t, run.execute(steps, nil).err)

	require.Len(t, run.launcher.launches, 1)
	certificates := run.launcher.launches[0].ClientCertificates
	require.Len(t, certificates, 2)
	assert.Equal(t, "with.browser.client_certificates[0]", certificates[0].Label)
	assert.Equal(t, []string{"portal.example.com"}, certificates[0].Hosts)
	assert.Equal(t, "browser.client_certificates[0] in the Dagu config", certificates[1].Label)
	assert.Equal(t, []string{"*"}, certificates[1].Hosts)
}

// A certificate that cannot be read fails the step before a browser starts,
// naming the entry without its passphrase.
func TestUnreadableClientCertificateFailsBeforeLaunch(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	steps := fmt.Sprintf(`{
		"browser": {"client_certificates": [{"hosts": ["portal.example.com"], "pfx_file": %q, "passphrase": "wrong-phrase"}]},
		"do": [{"act": "Click the checkout button"}]
	}`, writeClientCertificate(t, "portal", "right-phrase"))
	execution := run.execute(steps, nil)

	require.ErrorContains(t, execution.err, "with.browser.client_certificates[0]")
	assert.NotContains(t, execution.err.Error(), "wrong-phrase")
	assert.Empty(t, run.launcher.launches)
}

// A certificate's passphrase is kept from the model like a secret.
func TestClientCertificatePassphraseStaysFromModel(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	steps := fmt.Sprintf(`{
		"browser": {"client_certificates": [{"hosts": ["portal.example.com"], "pfx_file": %q, "passphrase": "s3cret-phrase"}]},
		"do": [{"act": "Type s3cret-phrase into the search box"}]
	}`, writeClientCertificate(t, "portal", "s3cret-phrase"))
	execution := run.execute(steps, nil)

	require.Error(t, execution.err)
	assert.NotContains(t, execution.err.Error(), "s3cret-phrase")
	assert.Empty(t, run.launcher.launches)
}

// A site that asks for a client certificate the browser does not present
// fails the goto with a hint, rather than leaving the browser on its error
// page.
func TestClientCertificateNeededFailsGoto(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	run.engine.outcome = pageOutcome{Code: "ERR_BAD_SSL_CLIENT_AUTH_CERT", Host: "portal.example.com"}
	execution := run.execute(`{"do": [{"goto": "https://portal.example.com"}]}`, nil)

	require.ErrorContains(t, execution.err, "portal.example.com asks for a TLS client certificate, and none is configured for it (ERR_BAD_SSL_CLIENT_AUTH_CERT)")
	assert.ErrorContains(t, execution.err, "with.browser.client_certificates")
}

// A failure the certificate proxy reports explains the error page the
// browser shows, and the step's log records it.
func TestCertificateProxyFailureExplainsErrorPage(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	run.hostCertificates = []cmnconfig.BrowserClientCertificate{
		{Hosts: []string{"portal.example.com"}, PFXFile: writeClientCertificate(t, "device", "")},
	}
	run.engine.outcome = pageOutcome{Code: "ERR_SSL_PROTOCOL_ERROR", Host: "portal.example.com"}
	run.engine.certificateEvents = []certproxy.Event{
		{Seq: 1, Kind: certproxy.EventConnect, Host: "portal.example.com", Message: "presented the certificate to portal.example.com", Presented: true},
		{Seq: 2, Kind: certproxy.EventClientCertificateRejected, Host: "portal.example.com", Message: "portal.example.com rejected the client certificate: bad certificate"},
	}
	execution := run.execute(`{"do": [{"goto": "https://portal.example.com"}]}`, nil)

	require.ErrorContains(t, execution.err, "portal.example.com rejected the client certificate: bad certificate (the browser shows ERR_SSL_PROTOCOL_ERROR)")
	assert.Contains(t, execution.stderr.String(), "client_certificate failed portal.example.com rejected the client certificate")
	assert.Contains(t, execution.stderr.String(), "client_certificate completed presented the certificate to portal.example.com")
}

// A failure the proxy reports for another host, such as a page's CDN, does
// not explain an error page unrelated to it.
func TestCertificateProxyFailureForAnotherHostIsNotBlamed(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	run.hostCertificates = []cmnconfig.BrowserClientCertificate{
		{Hosts: []string{"*"}, PFXFile: writeClientCertificate(t, "device", "")},
	}
	run.engine.outcome = pageOutcome{Code: "ERR_NAME_NOT_RESOLVED", Host: "portal.example.com"}
	run.engine.certificateEvents = []certproxy.Event{
		{Seq: 1, Kind: certproxy.EventUpstream, Host: "cdn.example.net", Message: "could not reach cdn.example.net"},
	}
	execution := run.execute(`{"do": [{"goto": "https://portal.example.com"}]}`, nil)

	require.NoError(t, execution.err)
}

// Without a host named on the error page, a proxy failure explains only an
// error a failed TLS connection causes.
func TestCertificateFailureWithoutHost(t *testing.T) {
	t.Parallel()

	failures := []certproxy.Event{{Kind: certproxy.EventClientCertificateRejected, Host: "portal.example.com", Message: "portal.example.com rejected the client certificate"}}
	require.ErrorContains(t, certificateFailure(pageOutcome{Code: "ERR_EMPTY_RESPONSE"}, failures, nil, true), "rejected the client certificate")
	require.NoError(t, certificateFailure(pageOutcome{Code: "ERR_NAME_NOT_RESOLVED"}, failures, nil, true))
	require.ErrorContains(t, certificateFailure(pageOutcome{Code: "ERR_SSL_PROTOCOL_ERROR", Host: "portal.example.com"}, failures, nil, true), "rejected")
	require.NoError(t, certificateFailure(pageOutcome{Code: "ERR_SSL_PROTOCOL_ERROR", Host: "other.example.com"}, failures, nil, true))
}

// A site that answers with an error page after asking for a certificate no
// entry names fails the goto, and says which site needs one.
func TestCertificateMissingFailsErrorStatus(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	guard := true
	run.hostGuard = &guard
	run.engine.outcome = pageOutcome{Host: "intranet.example.com", Status: http.StatusBadRequest, Title: "400 Bad Request"}
	run.engine.certificateEvents = []certproxy.Event{
		{Seq: 1, Kind: certproxy.EventClientCertificateMissing, Host: "intranet.example.com", Message: "intranet.example.com asks for a TLS client certificate, and none is configured for it"},
	}
	execution := run.execute(`{"do": [{"goto": "https://intranet.example.com"}]}`, nil)

	require.ErrorContains(t, execution.err, "intranet.example.com asks for a TLS client certificate, and none is configured for it (the site answered 400)")
	require.Len(t, run.launcher.launches, 1)
	assert.True(t, run.launcher.launches[0].Guard, "the host config turns the guard on without certificates")
	assert.Contains(t, execution.stderr.String(), "client_certificate asked intranet.example.com asks for a TLS client certificate")
}

// A site that asks for a certificate but opens without one is not a
// failure.
func TestCertificateMissingOnOpenPageIsNoFailure(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, pageModel(nil))
	guard := true
	run.hostGuard = &guard
	run.engine.outcome = pageOutcome{Host: "portal.example.com", Status: http.StatusOK, Title: "Portal"}
	run.engine.certificateEvents = []certproxy.Event{
		{Seq: 1, Kind: certproxy.EventClientCertificateMissing, Host: "portal.example.com", Message: "portal.example.com asks for a TLS client certificate, and none is configured for it"},
	}
	require.NoError(t, run.execute(`{"do": [{"goto": "https://portal.example.com"}]}`, nil).err)
}

// Without the proxy, a server's own refusal page still says the site needs
// a certificate; with a certificate presented there, it says the site
// refused it.
func TestServerRefusalPage(t *testing.T) {
	t.Parallel()

	nginx := pageOutcome{Host: "portal.example.com", Status: http.StatusBadRequest, Title: "400 No required SSL certificate was sent"}
	require.ErrorContains(t, certificateFailure(nginx, nil, nil, false),
		"portal.example.com asks for a TLS client certificate, and none is configured for it (it answered 400)")
	iis := pageOutcome{Host: "portal.example.com", Status: http.StatusForbidden, Title: "403 - Forbidden: Access is denied.", Text: "HTTP Error 403.16 - Forbidden"}
	require.ErrorContains(t, certificateFailure(iis, nil, map[string]string{"portal.example.com": "device"}, true),
		"portal.example.com rejected the client certificate device: it answered 403")
	notFound := pageOutcome{Host: "portal.example.com", Status: http.StatusNotFound, Title: "404 Not Found"}
	require.NoError(t, certificateFailure(notFound, nil, nil, true))
}

// An operation that times out names the connections the proxy still waits
// on.
func TestTimeoutNamesPendingConnections(t *testing.T) {
	t.Parallel()

	ts := newTestSessions(t, pageModel(nil))
	op := &operator{eng: ts.engine, certificateProxy: true}
	ts.engine.pending = []certproxy.Pending{
		{Host: "portal.example.com", Phase: certproxy.PhaseHandshake, Since: time.Now().Add(-40 * time.Second)},
		{Host: "cdn.example.net", Phase: certproxy.PhaseAnswer, Since: time.Now()},
	}
	err := op.explainWait(t.Context(), errBrowserUnresponsive)
	require.ErrorIs(t, err, errBrowserUnresponsive)
	assert.Contains(t, err.Error(), "still waiting on portal.example.com (TLS handshake with the site for 40s)")
	assert.NotContains(t, err.Error(), "cdn.example.net", "a connection that just started is not where the operation got stuck")
}

// A step resumed after an ask reattaches with the proxy its browser was
// launched with, without loading the certificates again.
func TestAskResumeKeepsCertificateProxy(t *testing.T) {
	t.Parallel()

	const steps = `{
		"do": [
			{"ask": {"prompt": "Enter the code", "as": "otp"}},
			{"act": "Type %otp% and submit"}
		]
	}`
	run := newTestRun(t, pageModel(nil))
	run.hostCertificates = []cmnconfig.BrowserClientCertificate{
		{Hosts: []string{"*"}, PFXFile: writeClientCertificate(t, "device", "")},
	}
	run.engine.handle.CertProxy = &certproxy.Handle{URL: "http://127.0.0.1:2/token", PID: 4343, StartedAt: 1_700_000_000_000}
	waiting := run.execute(steps, nil)
	require.NoError(t, waiting.err)
	records := run.records()
	require.Len(t, records, 1)
	assert.Equal(t, "http://127.0.0.1:2/token", records[0].CertProxyURL)

	// The certificate file is gone by the time the answer arrives; resuming
	// does not read it.
	run.hostCertificates[0].PFXFile = filepath.Join(t.TempDir(), "missing.p12")
	session := waiting.exec.GetAgentSession()
	session.Interactions[0].Status = ir.AgentInteractionAnswered
	session.Interactions[0].Answers = [][]string{{"123456"}}
	require.NoError(t, run.execute(steps, session).err)

	require.Len(t, run.launcher.reattaches, 1)
	require.NotNil(t, run.launcher.reattaches[0].CertProxy)
	assert.Equal(t, certproxy.Handle{URL: "http://127.0.0.1:2/token", PID: 4343, StartedAt: 1_700_000_000_000}, *run.launcher.reattaches[0].CertProxy)
}
