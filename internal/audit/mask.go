// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package audit

import (
	"context"
	"encoding/json"
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
	source SecretSource
	now    func() time.Time

	mu       sync.Mutex
	secrets  *masking.Masker
	revision uint64
	loadedAt time.Time
}

// NewMasker returns a masker that hides the values source supplies and
// common token formats. A nil source hides the token formats only.
func NewMasker(source SecretSource) *Masker {
	return &Masker{source: source, now: time.Now}
}

// Mask masks every field of entry but its control fields. Values are
// replaced whole, no field is cut, and details stays JSON. The entry is
// marked masked only when the secret values loaded; otherwise it keeps the
// values loaded last, carries no marker, and the next entry tries again.
func (m *Masker) Mask(ctx context.Context, entry *Entry) {
	secrets, loaded := m.current(ctx)
	// Token formats go first, so a stored value inside a token cannot
	// split the token and leave its rest unmatched.
	mask := func(text string) string {
		return secrets.MaskString(tokenPattern.ReplaceAllLiteralString(text, masking.DefaultMaskString))
	}
	value := reflect.ValueOf(entry).Elem()
	for _, index := range maskedFields() {
		field := value.FieldByIndex(index)
		if text := field.String(); text != "" {
			field.SetString(mask(text))
		}
	}
	if entry.Details != "" {
		entry.Details = maskDetails(entry.Details, mask)
	}
	entry.Masked = loaded
}

// maskDetails masks details, a JSON document, keeping it JSON: the smallest
// value holding a secret becomes a masked string, and in the rare document
// MaskJSON cannot keep JSON, the masked text is kept as a JSON string.
func maskDetails(details string, mask func(string) string) string {
	masked := masking.MaskJSON(details, mask)
	if json.Valid([]byte(masked)) || !json.Valid([]byte(details)) {
		return masked
	}
	// Encoding a string cannot fail.
	quoted, _ := json.Marshal(masked)
	return string(quoted)
}

// current returns the masker of the secret values and whether they loaded,
// reloading them when this process changed a secret or they are older than
// secretsMaxAge.
func (m *Masker) current(ctx context.Context) (*masking.Masker, bool) {
	if m == nil || m.source == nil {
		return nil, true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	revision := m.source.Revision()
	if !m.loadedAt.IsZero() && revision == m.revision && m.now().Sub(m.loadedAt) < secretsMaxAge {
		return m.secrets, true
	}
	values, err := m.source.CurrentValues(ctx)
	if err != nil {
		logger.Warn(ctx, "Failed to load secret values for audit masking", tag.Error(err))
		return m.secrets, false
	}
	m.secrets = masking.NewValueMasker(values)
	m.revision, m.loadedAt = revision, m.now()
	return m.secrets, true
}
