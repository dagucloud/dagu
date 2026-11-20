// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aiendpoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Limits of the models list (Spec 093, "The models list").
const (
	// MaxModelsResponseBytes is the largest response that the read accepts.
	MaxModelsResponseBytes = 4 << 20
	// MaxModels is the largest list that the server keeps. The read keeps the
	// first names of a longer list.
	MaxModels = 5000
	// MaxModelNameChars is the longest name of a model. A longer name is not kept.
	MaxModelNameChars = 256
	// MaxModelsPages is the largest number of pages that the read of the protocol
	// anthropic follows.
	MaxModelsPages = 10

	// modelsPageLimit is the query limit of a page of the protocol anthropic. It
	// is the largest value that the provider accepts.
	modelsPageLimit = 1000
	// AnthropicVersion is the value of the header anthropic-version of the read.
	AnthropicVersion = "2023-06-01"

	// baseURLText stands for the base URL of the endpoint in the models URL of a
	// preset.
	baseURLText = "{base_url}"
)

// Models is the models list of one endpoint, with the result of its last read.
// It is not part of the endpoint record, so a change of the endpoint never
// touches it. It never holds a key.
type Models struct {
	// Scope and Name identify the endpoint.
	Scope Scope
	Name  string
	// Names are the model names of the last good read, in the order of the provider.
	Names []string
	// Info holds the details that the provider gives for some of the names, in the
	// same order. Only the protocol anthropic gives details.
	Info []ModelInfo `json:",omitempty"`
	// RefreshedAt is the time of the last good read. It is zero before one.
	RefreshedAt time.Time
	// AttemptedAt is the time of the last read, good or not. The refresh period
	// counts from it.
	AttemptedAt time.Time
	// LastError is the text of the error of the last read. It is empty when the
	// last read was good.
	LastError   string
	LastErrorAt time.Time
}

// Clone returns a deep copy.
func (m *Models) Clone() *Models {
	if m == nil {
		return nil
	}
	clone := *m
	clone.Names = append([]string(nil), m.Names...)
	clone.Info = nil
	for _, info := range m.Info {
		info.Efforts = append([]string(nil), info.Efforts...)
		clone.Info = append(clone.Info, info)
	}
	return &clone
}

// ModelInfo holds the details of one model, as the protocol anthropic gives them.
type ModelInfo struct {
	// ID is the model name.
	ID string
	// DisplayName is the name that a person reads, for example "Claude Haiku 5.5".
	// It is empty when the provider gives none, or a bad one.
	DisplayName string `json:",omitempty"`
	// Efforts are the levels of effort that the model supports, from the lowest
	// to the highest. It is empty when the provider gives none.
	Efforts []string `json:",omitempty"`
}

// ModelsList is the result of one read of the models list.
type ModelsList struct {
	// Names are the model names, in the order of the provider.
	Names []string
	// Info holds the details of the models that have some. See Models.Info.
	Info []ModelInfo
}

// effortLevels is the ladder of the levels of effort, from the lowest.
var effortLevels = []string{"low", "medium", "high", "xhigh", "max"}

// ModelsStore keeps the models lists, next to the metadata of the endpoints. An
// implementation is safe for concurrent use.
type ModelsStore interface {
	// Get returns the list of the endpoint, or ErrNotFound when it has none.
	Get(ctx context.Context, scope Scope, name string) (*Models, error)
	// List returns the lists of all endpoints.
	List(ctx context.Context) ([]*Models, error)
	// Put saves the list of an endpoint, replacing the old one.
	Put(ctx context.Context, models *Models) error
	// Delete removes the list of an endpoint. It gives no error when there is none.
	Delete(ctx context.Context, scope Scope, name string) error
}

// ListURL returns the models URL of an endpoint, or an empty string when the
// endpoint has no list. A preset gives the URL as a text. The text {base_url}
// stands for the base URL of the endpoint. A URL that starts with the default
// base URL of the preset follows the base URL of the endpoint, so a change of the
// base URL moves the models URL too. The preset custom uses its field models_url.
func (e *Endpoint) ListURL() string {
	if e.Preset == PresetCustom {
		return e.ModelsURL
	}
	row, ok := LookupPreset(e.Preset, e.Protocol)
	if !ok || row.ModelsURL == "" {
		return ""
	}
	base := strings.TrimSuffix(e.BaseURL, "/")
	switch {
	case strings.Contains(row.ModelsURL, baseURLText):
		return strings.ReplaceAll(row.ModelsURL, baseURLText, base)
	case row.BaseURL != "" && strings.HasPrefix(row.ModelsURL, row.BaseURL):
		return base + strings.TrimPrefix(row.ModelsURL, row.BaseURL)
	}
	return row.ModelsURL
}

// ModelsReader reads the models list of an endpoint from the provider.
type ModelsReader struct {
	client  *http.Client
	timeout time.Duration
}

// NewModelsReader creates a reader. The client must not follow a redirect, and it
// holds the proxy and the TLS settings of the gateway. The timeout limits the
// whole read of one endpoint, all pages together.
func NewModelsReader(client *http.Client, timeout time.Duration) (*ModelsReader, error) {
	if client == nil {
		return nil, errors.New("models reader: client cannot be nil")
	}
	if timeout <= 0 {
		return nil, errors.New("models reader: timeout must be positive")
	}
	return &ModelsReader{client: client, timeout: timeout}, nil
}

// ErrNoModelsURL means that the endpoint has no models URL, so it has no list.
var ErrNoModelsURL = errors.New("the endpoint has no models URL")

// ReadList calls the models URL with the key and the authentication form of the
// endpoint. It returns the names of the models, and the details that the
// provider gives: the protocol anthropic gives the display name and the levels
// of effort of a model, the other protocols give none. An error never has the
// key.
func (r *ModelsReader) ReadList(ctx context.Context, ep Endpoint, key string) (list ModelsList, err error) {
	target := ep.ListURL()
	if target == "" {
		return ModelsList{}, ErrNoModelsURL
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	defer func() {
		if err != nil {
			err = scrubbed(err, key)
		}
	}()

	var all []ModelInfo
	after := ""
	for range MaxModelsPages {
		body, err := r.get(ctx, ep, target, key, after)
		if err != nil {
			return ModelsList{}, err
		}
		parsed, err := parseModelsPage(body)
		if err != nil {
			return ModelsList{}, err
		}
		all = append(all, parsed.models...)
		if ep.Protocol != ProtocolAnthropic || !parsed.hasMore {
			break
		}
		if parsed.lastID == "" || parsed.lastID == after {
			return ModelsList{}, errors.New("the response says that more models exist, but it gives no new cursor last_id")
		}
		after = parsed.lastID
	}
	list = normalizeModels(all)
	if ep.Protocol != ProtocolAnthropic {
		list.Info = nil
	}
	return list, nil
}

// get makes one call and returns the body of a good answer.
func (r *ModelsReader) get(ctx context.Context, ep Endpoint, target, key, after string) ([]byte, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, errors.New("the models URL is not valid")
	}
	if ep.Protocol == ProtocolAnthropic {
		query := url.Values{"limit": {strconv.Itoa(modelsPageLimit)}}
		if after != "" {
			query.Set("after_id", after)
		}
		u.RawQuery = query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errors.New("the models call cannot be built")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	if ep.Protocol == ProtocolAnthropic {
		req.Header.Set("Anthropic-Version", AnthropicVersion)
	}
	SetCredential(req.Header, ep.Auth, key)

	resp, err := r.client.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("the provider did not answer within %s", r.timeout)
		}
		if urlErr, ok := errors.AsType[*url.Error](err); ok {
			err = urlErr.Err
		}
		return nil, fmt.Errorf("the call to the provider failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return nil, fmt.Errorf("the provider answered with the redirect status %d, which the server does not follow", resp.StatusCode)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		// The body is not read: an error body can quote the key.
		return nil, fmt.Errorf("the provider answered with the status %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxModelsResponseBytes+1))
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("the provider did not answer within %s", r.timeout)
		}
		return nil, fmt.Errorf("the response could not be read: %w", err)
	}
	if len(body) > MaxModelsResponseBytes {
		return nil, fmt.Errorf("the response has more than %d bytes", MaxModelsResponseBytes)
	}
	return body, nil
}

type modelsPage struct {
	models  []ModelInfo
	hasMore bool
	lastID  string
}

// parseModelsPage reads the form {"data":[{"id":...}]} of the two protocols. The
// protocol anthropic adds has_more and last_id, and for each model display_name
// and capabilities.effort. A field of a model that is not valid is left out.
func parseModelsPage(body []byte) (modelsPage, error) {
	var raw struct {
		Data    json.RawMessage `json:"data"`
		HasMore bool            `json:"has_more"`
		LastID  *string         `json:"last_id"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return modelsPage{}, errors.New("the response is not a JSON object")
	}
	if len(raw.Data) == 0 || string(raw.Data) == "null" {
		return modelsPage{}, errors.New("the response has no field data")
	}
	var items []struct {
		ID           json.RawMessage `json:"id"`
		DisplayName  json.RawMessage `json:"display_name"`
		Capabilities struct {
			Effort json.RawMessage `json:"effort"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(raw.Data, &items); err != nil {
		return modelsPage{}, errors.New("the field data of the response is not a list of models")
	}
	page := modelsPage{hasMore: raw.HasMore}
	if raw.LastID != nil {
		page.lastID = *raw.LastID
	}
	for _, item := range items {
		var id string
		if json.Unmarshal(item.ID, &id) != nil {
			continue
		}
		info := ModelInfo{ID: id, Efforts: parseEfforts(item.Capabilities.Effort)}
		if json.Unmarshal(item.DisplayName, &info.DisplayName) != nil || !validDisplayName(info.DisplayName) {
			info.DisplayName = ""
		}
		page.models = append(page.models, info)
	}
	return page, nil
}

// parseEfforts returns the levels of effort that capabilities.effort marks as
// supported, in the order of the ladder. It returns nothing for a value that is
// not valid, or when the model does not support the effort.
func parseEfforts(raw json.RawMessage) []string {
	var effort map[string]json.RawMessage
	if json.Unmarshal(raw, &effort) != nil {
		return nil
	}
	if !supportedFlag(effort["supported"], true) {
		return nil
	}
	var levels []string
	for _, level := range effortLevels {
		if supportedFlag(effort[level], false) {
			levels = append(levels, level)
		}
	}
	return levels
}

// supportedFlag reads a field of the form {"supported":true}, or the boolean
// itself. A missing or not valid field gives fallback.
func supportedFlag(raw json.RawMessage, fallback bool) bool {
	var wrapped struct {
		Supported *bool `json:"supported"`
	}
	if json.Unmarshal(raw, &wrapped) == nil && wrapped.Supported != nil {
		return *wrapped.Supported
	}
	var plain bool
	if json.Unmarshal(raw, &plain) == nil {
		return plain
	}
	return fallback
}

// validDisplayName tells if a display name is kept: at most 256 characters, valid
// UTF-8 and no control character.
func validDisplayName(name string) bool {
	if name == "" || utf8.RuneCountInString(name) > MaxModelNameChars || !utf8.ValidString(name) {
		return false
	}
	return !strings.ContainsFunc(name, unicode.IsControl)
}

// normalizeModels keeps the models whose name is not empty and has at most 256
// characters, once each, in the order of the provider, up to 5000. It never
// changes a name. The details of a model with no display name and no effort are
// not kept.
func normalizeModels(models []ModelInfo) ModelsList {
	out := ModelsList{Names: make([]string, 0, len(models))}
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		name := model.ID
		if name == "" || utf8.RuneCountInString(name) > MaxModelNameChars || !utf8.ValidString(name) {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out.Names = append(out.Names, name)
		if model.DisplayName != "" || len(model.Efforts) > 0 {
			out.Info = append(out.Info, model)
		}
		if len(out.Names) == MaxModels {
			break
		}
	}
	return out
}

// scrubbed hides the key in an error. No call of the read puts the key in a
// message, so this is a second guard.
func scrubbed(err error, key string) error {
	if key == "" || !strings.Contains(err.Error(), key) {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), key, "[key]"))
}
