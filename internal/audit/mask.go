// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package audit

import (
	"context"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
)

// secretsMaxAge bounds how long a secret changed by another process, such as
// `dagu profile` or another server, can go unmasked.
const secretsMaxAge = time.Minute

// secretsRetryInterval bounds how often a store that fails to load is read
// again, so a broken record does not cost a full read on every entry.
const secretsRetryInterval = 10 * time.Second

// secretsLoadTimeout bounds a load of the secret values, which holds up every
// entry written meanwhile.
const secretsLoadTimeout = 5 * time.Second

// controlFields are the JSON names of the entry fields Dagu generates or
// picks from a fixed set. Mask leaves them as written, so no secret value
// can rewrite them. Every other text field is masked, fields added later
// included.
var controlFields = map[string]bool{
	"id":              true,
	"timestamp":       true,
	"category":        true,
	"action":          true,
	"source":          true,
	"surface":         true,
	"result":          true,
	"resource_type":   true,
	"credential_type": true,
	"credential_id":   true,
	"correlation_id":  true,
}

// detailsField is the JSON name of Entry.Details, which Mask keeps JSON.
const detailsField = "details"

// maskedFields holds the indexes of the Entry text fields Mask masks as
// plain text: all but the control fields and details.
var maskedFields = sync.OnceValue(func() [][]int {
	var fields [][]int
	for _, field := range reflect.VisibleFields(reflect.TypeFor[Entry]()) {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if field.Type.Kind() == reflect.String && !controlFields[name] && name != detailsField {
			fields = append(fields, field.Index)
		}
	}
	return fields
})

// tokenPattern matches common credential formats: GitHub tokens, AWS access
// key IDs, and Slack tokens.
var tokenPattern = regexp.MustCompile(
	`\bgh[pousr]_[A-Za-z0-9]{36,}\b` +
		`|\bgithub_pat_[A-Za-z0-9_]{22,}\b` +
		`|\b(?:AKIA|ASIA)[0-9A-Z]{16}\b` +
		`|\bxox[abposr]-[A-Za-z0-9-]{10,}`,
)

// tokenMasker masks the token formats alone, until secret values load.
var tokenMasker = masking.NewValueMasker(nil, tokenPattern)

// SecretSource supplies the secret values a Masker hides.
type SecretSource interface {
	// CurrentValues returns the current value of every secret to hide.
	CurrentValues(ctx context.Context) ([]string, error)
	// Revision changes whenever this process changes a secret value.
	Revision() uint64
}

// Masker hides secret values and common token formats in audit entries.
// It is safe for concurrent use.
type Masker struct {
	source      SecretSource
	now         func() time.Time
	loadTimeout time.Duration

	mu        sync.Mutex
	secrets   *masking.Masker
	revision  uint64
	checkedAt time.Time
	// loaded reports that the load at checkedAt succeeded.
	loaded bool
}

// NewMasker returns a masker that hides the values source supplies and
// common token formats. A nil source hides the token formats only and marks
// no entry masked.
func NewMasker(source SecretSource) *Masker {
	return &Masker{source: source, now: time.Now, loadTimeout: secretsLoadTimeout, secrets: tokenMasker}
}

// Mask masks every field of entry but its control fields. Values are
// replaced whole, no field is cut, and details stays JSON. The entry is
// marked masked only when the secret values loaded; otherwise it keeps the
// values loaded last and carries no marker. The values load even once ctx is
// cancelled, but not past its deadline.
func (m *Masker) Mask(ctx context.Context, entry *Entry) {
	secrets, loaded := m.current(ctx)
	value := reflect.ValueOf(entry).Elem()
	for _, index := range maskedFields() {
		field := value.FieldByIndex(index)
		if text := field.String(); text != "" {
			field.SetString(secrets.MaskString(text))
		}
	}
	if entry.Details != "" {
		entry.Details = secrets.MaskJSON(entry.Details)
	}
	entry.Masked = loaded
}

// current returns the masker of the secret values and token formats, and
// whether the values loaded. It reloads them when this process changed a
// secret, or when the last load is older than secretsMaxAge, or than
// secretsRetryInterval if it failed.
func (m *Masker) current(ctx context.Context) (*masking.Masker, bool) {
	if m == nil || m.source == nil {
		return tokenMasker, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	revision, now := m.source.Revision(), m.now()
	maxAge := secretsMaxAge
	if !m.loaded {
		maxAge = secretsRetryInterval
	}
	if !m.checkedAt.IsZero() && revision == m.revision && now.Sub(m.checkedAt) < maxAge {
		return m.secrets, m.loaded
	}
	// An entry can be written for a request that has already ended, as when
	// the server stops; the values load all the same, within their own time
	// and by the caller's deadline.
	timeout := m.loadTimeout
	if deadline, ok := ctx.Deadline(); ok {
		timeout = min(timeout, time.Until(deadline))
	}
	loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	values, err := m.source.CurrentValues(loadCtx)
	m.revision, m.checkedAt, m.loaded = revision, now, err == nil
	if err != nil {
		logger.Warn(ctx, "Failed to load secret values for audit masking", tag.Error(err))
		return m.secrets, false
	}
	m.secrets = masking.NewValueMasker(values, tokenPattern)
	return m.secrets, true
}
