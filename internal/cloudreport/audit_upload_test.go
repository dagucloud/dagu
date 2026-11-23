// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudreport

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/license"
	fileaudit "github.com/dagucloud/dagu/v2/internal/persis/file/audit"
	filemonitor "github.com/dagucloud/dagu/v2/internal/persis/file/monitor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixedSecrets is a secret source with fixed values.
type fixedSecrets []string

func (s fixedSecrets) CurrentValues(context.Context) ([]string, error) { return s, nil }
func (fixedSecrets) Revision() uint64                                  { return 0 }

// brokenSecrets is a secret source whose values fail to load.
type brokenSecrets struct{}

func (brokenSecrets) CurrentValues(context.Context) ([]string, error) {
	return nil, errors.New("secret store unreadable")
}
func (brokenSecrets) Revision() uint64 { return 0 }

// auditReceived is one request the fake Console received.
type auditReceived struct {
	path   string
	header http.Header
	// size is the decompressed body's size.
	size int
	body struct {
		Protocol int              `json:"protocol"`
		Entries  []auditWireEntry `json:"entries"`
		Gap      *auditGap        `json:"gap"`
	}
}

// ids returns the IDs of the entries the request carried.
func (r auditReceived) ids() []string {
	ids := make([]string, 0, len(r.body.Entries))
	for _, entry := range r.body.Entries {
		ids = append(ids, entry.ID)
	}
	return ids
}

// auditFakeConsole serves POST /api/v1/servers/audit. answer picks each
// response; without it, every request is stored.
type auditFakeConsole struct {
	t      *testing.T
	srv    *httptest.Server
	answer func(auditReceived) (status int, retryAfter string)

	mu       sync.Mutex
	received []auditReceived
	secret   string
}

func newAuditFakeConsole(t *testing.T) *auditFakeConsole {
	t.Helper()
	c := &auditFakeConsole{t: t, secret: "hb-secret"}
	c.srv = httptest.NewServer(http.HandlerFunc(c.serve))
	t.Cleanup(c.srv.Close)
	return c
}

func (c *auditFakeConsole) serve(w http.ResponseWriter, r *http.Request) {
	got := auditReceived{path: r.URL.Path, header: r.Header.Clone()}
	zr, err := gzip.NewReader(r.Body)
	if err != nil {
		http.Error(w, "not gzip", http.StatusBadRequest)
		return
	}
	data, err := io.ReadAll(zr)
	if err != nil {
		http.Error(w, "bad gzip", http.StatusBadRequest)
		return
	}
	got.size = len(data)
	if err := json.Unmarshal(data, &got.body); err != nil {
		http.Error(w, "bad JSON", http.StatusBadRequest)
		return
	}
	c.mu.Lock()
	c.received = append(c.received, got)
	answer := c.answer
	c.mu.Unlock()

	status, retry := http.StatusOK, ""
	if answer != nil {
		status, retry = answer(got)
	}
	if retry != "" {
		w.Header().Set("Retry-After", retry)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"stored":` + strconv.Itoa(len(got.body.Entries)) + `,"duplicates":0,"rejected":[]}`))
}

// credentials are the license the fake Console knows, with its current
// heartbeat secret.
func (c *auditFakeConsole) credentials() (license.CloudCredentials, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return license.CloudCredentials{CloudURL: c.srv.URL, LicenseID: "lic-1", ServerID: "srv-1", HeartbeatSecret: c.secret}, true
}

// requests returns the requests received so far and forgets them.
func (c *auditFakeConsole) requests() []auditReceived {
	c.mu.Lock()
	defer c.mu.Unlock()
	got := c.received
	c.received = nil
	return got
}

// sendNow sends batch with a sender masking with secrets.
func (c *auditFakeConsole) sendNow(secrets audit.SecretSource, batch auditBatch) error {
	creds, _ := c.credentials()
	return newAuditSender(secrets).send(c.t.Context(), creds, batch)
}

func maskedEntry(id string) *audit.Entry {
	entry := audit.NewEntry(audit.CategoryTerminal, "command", "u1", "ops")
	entry.ID, entry.Timestamp, entry.Masked = id, testClockStart, true
	return entry
}

func TestAuditSenderWireFormat(t *testing.T) {
	console := newAuditFakeConsole(t)
	gap := newAuditGap(audit.LossDeleted, testClockStart.Add(-time.Hour), testClockStart)

	require.NoError(t, console.sendNow(fixedSecrets{}, auditBatch{Entries: []*audit.Entry{maskedEntry("a")}, Gap: &gap}))

	got := console.requests()
	require.Len(t, got, 1)
	assert.Equal(t, auditPath, got[0].path)
	assert.Equal(t, "Bearer hb-secret", got[0].header.Get("Authorization"))
	assert.Equal(t, "lic-1", got[0].header.Get("Dagu-License-ID"))
	assert.Equal(t, "srv-1", got[0].header.Get("Dagu-Server-ID"))
	assert.Equal(t, "gzip", got[0].header.Get("Content-Encoding"))
	assert.Equal(t, auditProtocol, got[0].body.Protocol)
	assert.Equal(t, []string{"a"}, got[0].ids())
	assert.True(t, got[0].body.Entries[0].Masked)
	assert.Equal(t, &gap, got[0].body.Gap)
}

// Entries are masked again with the values current when they are sent, so a
// secret added after an entry was written does not leave the server, and
// masking comes before any cut (RFC 021 Verification 13 and 14).
func TestAuditSenderMasksAgainBeforeCutting(t *testing.T) {
	console := newAuditFakeConsole(t)
	secret := "tok_" + strings.Repeat("Z", 30)
	// A 300-byte URI whose secret runs across byte 256.
	prefix := "dagu://dags/" + strings.Repeat("u", 228)
	uri := prefix + secret + strings.Repeat("v", 26)
	entry := maskedEntry("a")
	entry.Workspace, entry.Username, entry.UserID, entry.ResourceID = "ws-"+secret, secret, "basic:"+secret, uri
	entry.Details = `{"command":"export TOKEN=` + secret + `"}`

	require.NoError(t, console.sendNow(fixedSecrets{secret}, auditBatch{Entries: []*audit.Entry{entry}}))

	sent := console.requests()[0].body.Entries[0]
	for _, text := range []string{sent.Workspace, sent.Username, sent.UserID, sent.ResourceID, sent.Details} {
		assert.NotContains(t, text, "tok_")
	}
	assert.Equal(t, (prefix + masked + strings.Repeat("v", 26))[:auditFieldBytes], sent.ResourceID)
	assert.JSONEq(t, `{"command":"export TOKEN=`+masked+`"}`, sent.Details)
	assert.Contains(t, entry.Details, secret, "the caller's entry is left as it was")
}

// masked is what a masked value reads as.
const masked = "*******"

// A field past its limit is cut and named in truncated, and NUL, which
// Console cannot store, is replaced (RFC 021 Verification 19).
func TestAuditSenderCutsFieldsToTheirLimits(t *testing.T) {
	console := newAuditFakeConsole(t)
	long := maskedEntry("long")
	long.Username = strings.Repeat("é", 150<<10)
	long.Action = strings.Repeat("a", 100)
	withNUL := maskedEntry("nul")
	withNUL.Username = "op\x00s"

	require.NoError(t, console.sendNow(fixedSecrets{}, auditBatch{Entries: []*audit.Entry{long, withNUL}}))

	sent := console.requests()[0].body.Entries
	assert.Len(t, sent[0].Username, auditFieldBytes)
	assert.Len(t, sent[0].Action, auditNameBytes)
	assert.ElementsMatch(t, []string{"username", "action"}, sent[0].Truncated)
	assert.Equal(t, "op\uFFFDs", sent[1].Username)
	assert.Empty(t, sent[1].Truncated)
}

// Escaping can nearly double an entry as JSON, so details is cut until the
// encoded entry fits (RFC 021 Verification 20).
func TestAuditSenderCutsDetailsToFitTheEntry(t *testing.T) {
	console := newAuditFakeConsole(t)
	entry := maskedEntry("a")
	command, err := json.Marshal(map[string]string{"command": strings.Repeat(`\`, 8<<10)})
	require.NoError(t, err)
	entry.Details = string(command)

	require.NoError(t, console.sendNow(fixedSecrets{}, auditBatch{Entries: []*audit.Entry{entry}}))

	sent := console.requests()[0].body.Entries[0]
	encoded, err := json.Marshal(sent)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(encoded), auditEntryBytes)
	assert.Greater(t, len(encoded), auditEntryBytes-64, "details keeps what fits")
	assert.Equal(t, []string{"details"}, sent.Truncated)
}

// A batch over Console's request limit goes as several requests, each
// within it, the gap with the first.
func TestAuditSenderSplitsLargeBatches(t *testing.T) {
	console := newAuditFakeConsole(t)
	var entries []*audit.Entry
	for i := range 600 {
		entry := maskedEntry(strconv.Itoa(i))
		entry.Details = `{"command":"` + strings.Repeat("x", 10<<10) + `"}`
		entries = append(entries, entry)
	}
	gap := newAuditGap(audit.LossRejected, testClockStart, testClockStart)

	require.NoError(t, console.sendNow(fixedSecrets{}, auditBatch{Entries: entries, Gap: &gap}))

	got := console.requests()
	require.Greater(t, len(got), 1)
	var ids []string
	for i, request := range got {
		assert.LessOrEqual(t, request.size, 4<<20)
		assert.Equal(t, i == 0, request.body.Gap != nil)
		ids = append(ids, request.ids()...)
	}
	assert.Len(t, ids, 600)
}

// Console refusing a request as too large halves it, and a single entry it
// still refuses goes as a rejected gap, so the entries after it are sent
// (RFC 021 Verification 21).
func TestAuditSenderHalvesWhatConsoleFindsTooLarge(t *testing.T) {
	console := newAuditFakeConsole(t)
	console.answer = func(r auditReceived) (int, string) {
		if slices.Contains(r.ids(), "huge") {
			return http.StatusRequestEntityTooLarge, ""
		}
		return http.StatusOK, ""
	}
	huge := maskedEntry("huge")
	huge.Timestamp = testClockStart.Add(time.Minute)

	require.NoError(t, console.sendNow(fixedSecrets{}, auditBatch{Entries: []*audit.Entry{maskedEntry("a"), huge, maskedEntry("b")}}))

	var stored []string
	var gaps []*auditGap
	for _, request := range console.requests() {
		if slices.Contains(request.ids(), "huge") {
			continue
		}
		stored = append(stored, request.ids()...)
		if request.body.Gap != nil {
			gaps = append(gaps, request.body.Gap)
		}
	}
	assert.Equal(t, []string{"a", "b"}, stored)
	require.Len(t, gaps, 1)
	assert.Equal(t, audit.LossRejected, gaps[0].Reason)
	assert.Equal(t, huge.Timestamp, gaps[0].From)
	assert.Equal(t, huge.Timestamp, gaps[0].To)
}

// Entries are not sent while the secret values to mask them with fail to
// load.
func TestAuditSenderHoldsWhileSecretsFailToLoad(t *testing.T) {
	console := newAuditFakeConsole(t)

	err := console.sendNow(brokenSecrets{}, auditBatch{Entries: []*audit.Entry{maskedEntry("a")}})

	var refusal *auditRefusal
	require.ErrorAs(t, err, &refusal)
	assert.Equal(t, auditMaskRetry, refusal.wait)
	assert.Empty(t, console.requests())
}

// startSendingShipper runs a shipper over d that sends to console over HTTP.
func startSendingShipper(t *testing.T, d *auditDir, console *auditFakeConsole) (*auditShipper, *fakeClock) {
	t.Helper()
	s := newAuditShipper(fileaudit.NewFeed(d.dir), d.state, filemonitor.NewLease(d.stateFile, nil),
		config.AuditAll, newAuditSender(noSecrets{}).send)
	s.credentials, s.licensed = console.credentials, func() bool { return true }
	clock := &fakeClock{t: t, waits: make(chan time.Duration, 16), fire: make(chan time.Time), at: testClockStart}
	s.after, s.now = clock.after, clock.now
	s.start(t.Context())
	t.Cleanup(s.stop)
	require.Equal(t, auditCheckInterval, clock.wait())
	return s, clock
}

// Each answer that holds sending holds it as long as it says, and the same
// entries go once it is over (RFC 021 Verification 5).
func TestAuditShipperHoldsAsConsoleAnswers(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		retry  string
		held   time.Duration
	}{
		{"busy", http.StatusServiceUnavailable, "120", 2 * time.Minute},
		{"over the pace or quota", http.StatusTooManyRequests, "3600", time.Hour},
		{"plan without audit logs", http.StatusPaymentRequired, "", auditPlanRetry},
		{"failure", http.StatusInternalServerError, "", initialBackoff},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newAuditDir(t)
			entry := d.write(t, testClockStart, audit.NewEntry(audit.CategoryTerminal, "command", "u1", "ops"))
			console := newAuditFakeConsole(t)
			refused := true
			console.answer = func(auditReceived) (int, string) {
				if refused {
					refused = false
					return tc.status, tc.retry
				}
				return http.StatusOK, ""
			}

			_, clock := startSendingShipper(t, d, console)
			require.Len(t, console.requests(), 1)

			clock.shift(tc.held - 2*auditCheckInterval)
			clock.next()
			assert.Empty(t, console.requests(), "held")

			clock.next()
			got := console.requests()
			require.Len(t, got, 1)
			assert.Equal(t, []string{entry.ID}, got[0].ids())
		})
	}
}

// A license Console refuses sends nothing until it changes.
func TestAuditShipperWaitsForANewLicense(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusGone} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			d := newAuditDir(t)
			d.write(t, testClockStart, audit.NewEntry(audit.CategoryTerminal, "command", "u1", "ops"))
			console := newAuditFakeConsole(t)
			console.answer = func(r auditReceived) (int, string) {
				if r.header.Get("Authorization") == "Bearer hb-secret" {
					return status, ""
				}
				return http.StatusOK, ""
			}

			_, clock := startSendingShipper(t, d, console)
			require.Len(t, console.requests(), 1)
			clock.shift(2 * time.Hour)
			clock.next()
			assert.Empty(t, console.requests())

			console.mu.Lock()
			console.secret = "hb-renewed"
			console.mu.Unlock()
			clock.next()
			assert.Len(t, console.requests(), 1)
		})
	}
}

// A server that is no longer connected to Dagu Console, or whose license no
// longer includes audit logs, reads no audit file and frees the lease.
func TestAuditShipperIdlesWithoutConsole(t *testing.T) {
	for name, off := range map[string]func(connected, licensed *atomic.Bool){
		"not connected": func(connected, _ *atomic.Bool) { connected.Store(false) },
		"no audit logs": func(_, licensed *atomic.Bool) { licensed.Store(false) },
	} {
		t.Run(name, func(t *testing.T) {
			d := newAuditDir(t)
			d.write(t, testClockStart, audit.NewEntry(audit.CategoryTerminal, "command", "u1", "ops"))
			console := newAuditConsole()
			feed := &countingFeed{AuditFeed: fileaudit.NewFeed(d.dir)}
			var connected, licensed atomic.Bool
			connected.Store(true)
			licensed.Store(true)
			s := newAuditShipper(feed, d.state, filemonitor.NewLease(d.stateFile, nil), config.AuditAll, console.send)
			s.credentials = func() (license.CloudCredentials, bool) {
				creds, _ := testCredentials()
				return creds, connected.Load()
			}
			s.licensed = licensed.Load
			clock := &fakeClock{t: t, waits: make(chan time.Duration, 16), fire: make(chan time.Time), at: testClockStart}
			s.after, s.now = clock.after, clock.now
			s.start(t.Context())
			t.Cleanup(s.stop)
			clock.wait()
			require.Len(t, console.sent(), 1)

			off(&connected, &licensed)
			reads := feed.reads.Load()
			d.write(t, testClockStart, audit.NewEntry(audit.CategoryTerminal, "command", "u1", "ops"))
			clock.next()
			clock.next()

			assert.Equal(t, reads, feed.reads.Load())
			assert.Empty(t, console.sent())
			lease := filemonitor.NewLease(d.stateFile, nil)
			require.NoError(t, lease.TryLock(), "the lease is free")
			require.NoError(t, lease.Unlock())
		})
	}
}
