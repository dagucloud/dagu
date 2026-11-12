// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package certproxy presents TLS client certificates on behalf of a browser.
// The browser sends its connections to the certificate hosts through a local
// proxy, which ends the browser's TLS with a certificate the browser is told
// to accept and opens TLS to the site with the client certificate, routed
// the way the device routes its own traffic.
//
//	browser ── PAC ──┬─ https: certificate host ── proxy ── TLS + client cert ── site
//	                 └─ anything else ───────────── the device's own proxy setup
//
// Chrome does not consult a PAC script for localhost names, loopback
// addresses, or link-local addresses. When an entry names one of those, the
// browser uses a manual proxy for every request instead, and this proxy
// forwards the others on the device's own route.
//
// The proxy runs as a process of its own, because the browser outlives the
// process that launched it, and exits once that browser is gone.
package certproxy

import (
	"maps"
	"slices"
	"sync"
	"time"
)

// Entry is a client certificate ready for the proxy, and the hosts it is
// presented to.
type Entry struct {
	// Label names the entry in messages, such as
	// with.browser.client_certificates[0].
	Label string `json:"label"`
	// Hosts are host patterns, as the hostpattern package matches them.
	Hosts []string `json:"hosts"`
	// ChainPEM holds the certificate, then its intermediates.
	ChainPEM string `json:"chain_pem"`
	// KeyPEM holds the certificate's private key as PKCS#8.
	KeyPEM string `json:"key_pem"`
	// CAPEM holds certificates trusted, beside the system's, for the hosts'
	// servers.
	CAPEM string `json:"ca_pem,omitempty"`
}

// TrustedCA is a server certificate authority trusted only for the hosts it
// names.
type TrustedCA struct {
	// Label names the authority in messages, such as browser.trusted_cas[0].
	Label string `json:"label"`
	// Hosts are host patterns, as the hostpattern package matches them.
	Hosts []string `json:"hosts"`
	// CAPEM holds the authority's certificates.
	CAPEM string `json:"ca_pem"`
}

// Config is what the proxy serves, and for which browser.
type Config struct {
	// Token is a random value that prefixes the proxy's control paths.
	Token string `json:"token"`
	// CDPURL is the DevTools address of the browser the proxy serves, such
	// as http://127.0.0.1:9333.
	CDPURL string `json:"cdp_url"`
	// SystemPAC is a PAC script for the device's own routing. Empty routes
	// everything directly.
	SystemPAC string `json:"system_pac"`
	// Entries are the certificates; the first whose hosts match a host is
	// presented to it.
	Entries []Entry `json:"entries"`
	// TrustedCAs add server trust to the hosts each names. Every matching
	// authority applies, beside the entry presented there, if any.
	TrustedCAs []TrustedCA `json:"trusted_cas,omitempty"`
	// GraceSeconds is how long the proxy waits for the browser to answer
	// for the first time; zero means five minutes.
	GraceSeconds int `json:"grace_seconds,omitempty"`
	// Guard sends the browser's other https: requests through the proxy too,
	// so the browser never answers a site's request for a client
	// certificate itself: a site that may ask for one gets the proxy's TLS,
	// presenting none, and the others the browser's own.
	Guard bool `json:"guard,omitempty"`
}

// Ready describes a proxy that accepts connections.
type Ready struct {
	Port int `json:"port"`
	// SPKI is the base64 SHA-256 hash of the public key of every
	// certificate the proxy shows the browser, in the form Chrome's
	// --ignore-certificate-errors-spki-list takes.
	SPKI string `json:"spki"`
	// PACURL is the PAC script the browser uses: a data: URL, or one the
	// proxy serves when the script is too large for a command line.
	PACURL string `json:"pac_url"`
	// GuardPort is the port of the guard, or zero without one.
	GuardPort int `json:"guard_port,omitempty"`
	// Manual reports that the browser must send every request to the
	// proxy. A PAC script cannot send Chrome to localhost names, loopback
	// addresses, or link-local addresses.
	Manual bool `json:"manual,omitempty"`
}

// Kinds of Event.
const (
	// EventConnect reports the first connection to a host an entry or an
	// authority names, once its server certificate was verified.
	EventConnect = "connect"
	// EventClientCertificateRejected reports a server that refused the
	// client certificate.
	EventClientCertificateRejected = "client_certificate_rejected"
	// EventServerCertificate reports a server whose certificate is not
	// trusted.
	EventServerCertificate = "server_certificate"
	// EventProxyAuth reports an upstream proxy that asks for credentials.
	EventProxyAuth = "proxy_auth"
	// EventUpstream reports a server the proxy could not reach.
	EventUpstream = "upstream"
	// EventRefused reports a connection to a host no entry names.
	EventRefused = "refused"
	// EventClientCertificateMissing reports a server that asked for a
	// client certificate when no entry names its host.
	EventClientCertificateMissing = "client_certificate_missing"
	// EventGuardOff reports that the guard stopped, because the device's
	// proxy asks for credentials the proxy cannot give.
	EventGuardOff = "guard_off"
)

// Pending is a connection the proxy has not yet heard the site answer on.
type Pending struct {
	Host  string    `json:"host"`
	Phase string    `json:"phase"`
	Since time.Time `json:"since"`
}

// Phases of a pending connection.
const (
	PhaseReaching  = "reaching the site"
	PhaseHandshake = "TLS handshake with the site"
	PhaseAnswer    = "waiting for the site's answer"
)

// Event is something the proxy reports about a connection.
type Event struct {
	Seq  int64     `json:"seq"`
	Time time.Time `json:"time"`
	Kind string    `json:"kind"`
	Host string    `json:"host"`
	// Entry is the label of the entry involved, if any.
	Entry string `json:"entry,omitempty"`
	// Message explains the event in a sentence of its own.
	Message string `json:"message,omitempty"`
	// Presented is set on a connection on which the entry's certificate
	// was presented.
	Presented bool `json:"presented,omitempty"`
}

const (
	// maxEvents bounds the events kept for readers.
	maxEvents = 200
	// repeatWindow is how long an event repeated by the browser's retries
	// is reported once.
	repeatWindow = 10 * time.Second
)

// eventLog keeps the latest events, without repeats.
type eventLog struct {
	mu     sync.Mutex
	seq    int64
	events []Event
	// recent holds when each event was last reported.
	recent map[eventKey]time.Time
	// connected holds the hosts each entry connected to.
	connected map[connectKey]bool
}

type eventKey struct {
	kind, host, message string
}

type connectKey struct {
	entry, host string
}

func newEventLog() *eventLog {
	return &eventLog{recent: map[eventKey]time.Time{}, connected: map[connectKey]bool{}}
}

// add records an event unless the same one was recorded within
// repeatWindow.
func (l *eventLog) add(kind, host, entry, message string) {
	l.record(Event{Kind: kind, Host: host, Entry: entry, Message: message})
}

// record keeps event, numbered and timed, unless the same one was recorded
// within repeatWindow.
func (l *eventLog) record(event Event) {
	now := time.Now()
	key := eventKey{kind: event.Kind, host: event.Host, message: event.Message}
	l.mu.Lock()
	defer l.mu.Unlock()
	if last, ok := l.recent[key]; ok && now.Sub(last) < repeatWindow {
		return
	}
	for k, last := range l.recent {
		if now.Sub(last) >= repeatWindow {
			delete(l.recent, k)
		}
	}
	l.recent[key] = now
	l.seq++
	event.Seq, event.Time = l.seq, now
	l.events = append(l.events, event)
	if len(l.events) > maxEvents {
		l.events = append(l.events[:0:0], l.events[len(l.events)-maxEvents:]...)
	}
}

// connect records the first connection entry makes to host, and whether
// it presented its certificate there.
func (l *eventLog) connect(host, entry string, presented bool, message string) {
	key := connectKey{entry: entry, host: host}
	l.mu.Lock()
	seen := l.connected[key]
	l.connected[key] = true
	l.mu.Unlock()
	if !seen {
		l.record(Event{Kind: EventConnect, Host: host, Entry: entry, Message: message, Presented: presented})
	}
}

// verifiedHosts holds the hosts whose server certificate the proxy
// verified, each mapped to whether the guard carried the connection. The
// latest verification of a host decides.
type verifiedHosts struct {
	mu    sync.Mutex
	hosts map[string]bool
	// guardOff is set once the guard stopped.
	guardOff bool
}

func newVerifiedHosts() *verifiedHosts {
	return &verifiedHosts{hosts: map[string]bool{}}
}

// add records a verified host, unless the guard carried it after it
// stopped: the browser reaches that host directly from then on.
func (v *verifiedHosts) add(host string, guarded bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if guarded && v.guardOff {
		return
	}
	v.hosts[host] = guarded
}

func (v *verifiedHosts) remove(host string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.hosts, host)
}

// dropGuarded forgets the hosts the guard carried, which the browser
// reaches itself once the guard stops.
func (v *verifiedHosts) dropGuarded() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.guardOff = true
	maps.DeleteFunc(v.hosts, func(_ string, guarded bool) bool { return guarded })
}

func (v *verifiedHosts) has(host string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	_, ok := v.hosts[host]
	return ok
}

// after returns the events recorded after seq.
func (l *eventLog) after(seq int64) []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	events := []Event{}
	for _, event := range l.events {
		if event.Seq > seq {
			events = append(events, event)
		}
	}
	return events
}

// pendingLog keeps the connections the proxy has not yet heard the site
// answer on.
type pendingLog struct {
	mu    sync.Mutex
	next  int64
	conns map[int64]Pending
}

func newPendingLog() *pendingLog {
	return &pendingLog{conns: map[int64]Pending{}}
}

// start records a connection to host and returns its ID.
func (p *pendingLog) start(host string) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.next++
	p.conns[p.next] = Pending{Host: host, Phase: PhaseReaching, Since: time.Now()}
	return p.next
}

// set moves a connection to phase, restarting its time.
func (p *pendingLog) set(id int64, phase string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if conn, ok := p.conns[id]; ok {
		conn.Phase, conn.Since = phase, time.Now()
		p.conns[id] = conn
	}
}

// end forgets a connection; ending one twice is harmless.
func (p *pendingLog) end(id int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.conns, id)
}

// list returns the pending connections, the longest waiting first.
func (p *pendingLog) list() []Pending {
	p.mu.Lock()
	defer p.mu.Unlock()
	conns := slices.Collect(maps.Values(p.conns))
	slices.SortFunc(conns, func(a, b Pending) int { return a.Since.Compare(b.Since) })
	if conns == nil {
		conns = []Pending{}
	}
	return conns
}
