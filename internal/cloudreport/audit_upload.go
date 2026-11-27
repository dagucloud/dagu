// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudreport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/license"
)

const (
	auditPath     = "/api/v1/servers/audit"
	auditProtocol = 1
	// auditRequestBytes keeps a request's entries under Console's 4 MiB
	// limit on a decompressed body, with room for the envelope and a gap.
	auditRequestBytes = 4<<20 - 64<<10
	// auditEntryBytes is the most an entry may take as encoded JSON.
	auditEntryBytes = 16 << 10
	// auditFieldBytes is the most a field other than details may take; an
	// auditNameBytes field, less.
	auditFieldBytes = 256
	auditNameBytes  = 64
	// auditPlanRetry is how often a plan without audit logs asks again.
	auditPlanRetry = time.Hour
	// auditMaskRetry is how long a batch waits when the secret values to
	// mask it with fail to load.
	auditMaskRetry = 10 * time.Second
)

// auditRefusal is an answer that holds sending: for a while, or until the
// license changes.
type auditRefusal struct {
	msg   string
	wait  time.Duration
	until bool
}

func (r *auditRefusal) Error() string { return r.msg }

// auditSender posts batches to Dagu Console in the upload format.
type auditSender struct {
	client *http.Client
	masker *audit.Masker
}

// auditRequest is the body of POST /api/v1/servers/audit.
type auditRequest struct {
	Protocol int               `json:"protocol"`
	Entries  []json.RawMessage `json:"entries"`
	Gap      *auditGap         `json:"gap,omitempty"`
}

// auditWireEntry is an entry as Console takes it, with the fields cut to
// their limits.
type auditWireEntry struct {
	audit.Entry
	Truncated []string `json:"truncated,omitempty"`
}

// auditUpload is an entry ready to send.
type auditUpload struct {
	data json.RawMessage
	at   time.Time
}

// auditAnswer is Console's answer to a stored batch.
type auditAnswer struct {
	Stored     int      `json:"stored"`
	Duplicates int      `json:"duplicates"`
	Rejected   []string `json:"rejected"`
}

func newAuditSender(secrets audit.SecretSource) *auditSender {
	return &auditSender{client: &http.Client{Timeout: requestTimeout}, masker: audit.NewMasker(secrets)}
}

// send posts batch and returns nil once Console has stored all of it. A
// batch too large for one request goes as several, in order, its gap with
// the first.
func (u *auditSender) send(ctx context.Context, creds license.CloudCredentials, batch auditBatch) error {
	uploads := make([]auditUpload, 0, len(batch.Entries))
	for _, entry := range batch.Entries {
		upload, ok := u.prepare(ctx, entry)
		if !ok {
			return &auditRefusal{msg: "the secret values to mask audit entries with failed to load", wait: auditMaskRetry}
		}
		uploads = append(uploads, upload)
	}

	gap := batch.Gap
	for len(uploads) > 0 || gap != nil {
		n, size := 0, 0
		for n < len(uploads) && (n == 0 || size+len(uploads[n].data) <= auditRequestBytes) {
			size += len(uploads[n].data) + 1
			n++
		}
		if err := u.post(ctx, creds, uploads[:n], gap); err != nil {
			return err
		}
		uploads, gap = uploads[n:], nil
	}
	return nil
}

// post sends one request. Console refusing it as too large halves it; a
// single entry still refused goes as a rejected gap in its place.
func (u *auditSender) post(ctx context.Context, creds license.CloudCredentials, uploads []auditUpload, gap *auditGap) error {
	res, err := u.do(ctx, creds, uploads, gap)
	if err != nil {
		return err
	}
	switch status := res.status; {
	case status >= 200 && status < 300:
		var answer auditAnswer
		if json.Unmarshal(res.body, &answer) == nil && len(answer.Rejected) > 0 {
			logger.Warn(ctx, "Dagu Console refused audit entries it cannot store and recorded a gap for them",
				slog.Any("ids", answer.Rejected))
		}
		return nil
	case status == http.StatusRequestEntityTooLarge && len(uploads) > 1:
		half := len(uploads) / 2
		if err := u.post(ctx, creds, uploads[:half], gap); err != nil {
			return err
		}
		return u.post(ctx, creds, uploads[half:], nil)
	case status == http.StatusRequestEntityTooLarge && len(uploads) == 1:
		if gap != nil {
			if err := u.post(ctx, creds, nil, gap); err != nil {
				return err
			}
		}
		lone := newAuditGap(audit.LossRejected, uploads[0].at, uploads[0].at)
		return u.post(ctx, creds, nil, &lone)
	case status == http.StatusTooManyRequests, status == http.StatusServiceUnavailable:
		return &auditRefusal{msg: fmt.Sprintf("Dagu Console asked to wait (status %d)", status), wait: retryAfter(res.retryAfter)}
	case status == http.StatusUnauthorized, status == http.StatusGone:
		return &auditRefusal{msg: fmt.Sprintf("Dagu Console refused the license (status %d)", status), until: true}
	case status == http.StatusPaymentRequired:
		return &auditRefusal{msg: "the plan does not include audit logs in Dagu Console", wait: auditPlanRetry}
	default:
		return fmt.Errorf("dagu console answered %d: %s", status, res.message())
	}
}

// do posts uploads and gap, gzip-compressed, with the license's credentials
// as headers, so that Console checks them before it reads the body.
func (u *auditSender) do(ctx context.Context, creds license.CloudCredentials, uploads []auditUpload, gap *auditGap) (*response, error) {
	request := auditRequest{Protocol: auditProtocol, Entries: make([]json.RawMessage, 0, len(uploads)), Gap: gap}
	for _, upload := range uploads {
		request.Entries = append(request.Entries, upload.data)
	}
	data, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode audit request: %w", err)
	}
	body, err := gzipped(data)
	if err != nil {
		return nil, fmt.Errorf("compress audit request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, creds.CloudURL+auditPath, body)
	if err != nil {
		return nil, fmt.Errorf("build audit request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+creds.HeartbeatSecret)
	req.Header.Set("Dagu-License-ID", creds.LicenseID)
	req.Header.Set("Dagu-Server-ID", creds.ServerID)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("User-Agent", "dagu-oss/"+config.Version)
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	answer, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return nil, fmt.Errorf("read audit answer: %w", err)
	}
	return &response{status: resp.StatusCode, retryAfter: resp.Header.Get("Retry-After"), body: answer}, nil
}

// prepare masks a copy of entry with the current secret values, replaces
// NUL characters, which Console cannot store, cuts each field to its limit,
// and cuts details until the encoded entry fits. Masking comes first, so a
// cut never leaves part of a secret. It reports false when the secret
// values fail to load.
func (u *auditSender) prepare(ctx context.Context, entry *audit.Entry) (auditUpload, bool) {
	wire := auditWireEntry{Entry: *entry}
	u.masker.Mask(ctx, &wire.Entry)
	if !wire.Masked {
		return auditUpload{}, false
	}

	value := reflect.ValueOf(&wire.Entry).Elem()
	for _, field := range auditTextFields() {
		text := strings.ReplaceAll(value.FieldByIndex(field.index).String(), "\x00", "\uFFFD")
		if field.name != "details" {
			limit := auditFieldBytes
			if field.short {
				limit = auditNameBytes
			}
			if len(text) > limit {
				text = cutUTF8(text, limit)
				wire.Truncated = append(wire.Truncated, field.name)
			}
		}
		value.FieldByIndex(field.index).SetString(text)
	}

	data, _ := json.Marshal(wire)
	if len(data) > auditEntryBytes && wire.Details != "" {
		data = fitDetails(&wire)
	}
	return auditUpload{data: data, at: entry.Timestamp}, true
}

// fitDetails cuts wire's details to the longest start that keeps the encoded
// entry within auditEntryBytes, and returns the encoded entry. Escaping can
// take a byte of details up to six as JSON, so the cut is searched for.
func fitDetails(wire *auditWireEntry) []byte {
	details := wire.Details
	wire.Truncated = append(wire.Truncated, "details")
	encode := func(n int) []byte {
		wire.Details = cutUTF8(details, n)
		data, _ := json.Marshal(wire)
		return data
	}
	fits, tooLong := 0, len(details)
	for tooLong-fits > 1 {
		mid := fits + (tooLong-fits)/2
		if len(encode(mid)) <= auditEntryBytes {
			fits = mid
		} else {
			tooLong = mid
		}
	}
	return encode(fits)
}

// auditTextField is a text field of audit.Entry, by its JSON name. A short
// one is cut to auditNameBytes, details only to fit the entry, and the rest
// to auditFieldBytes.
type auditTextField struct {
	name  string
	index []int
	short bool
}

// auditTextFields lists the text fields of audit.Entry.
var auditTextFields = sync.OnceValue(func() []auditTextField {
	entryType := reflect.TypeFor[audit.Entry]()
	var fields []auditTextField
	for _, field := range reflect.VisibleFields(entryType) {
		if field.Type.Kind() != reflect.String {
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		fields = append(fields, auditTextField{
			name:  name,
			index: field.Index,
			short: name == "category" || name == "action",
		})
	}
	return fields
})

// cutUTF8 returns s cut to at most n bytes, at a character boundary.
func cutUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
