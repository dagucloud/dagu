// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package extauthz

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	evaluationPath    = "/access/v1/evaluation"
	evaluationsPath   = "/access/v1/evaluations"
	maxPDPAnswerBytes = 1 << 20
	maxBatchSize      = 100
	maxCacheEntries   = 10000
)

// Subject is an AuthZEN subject.
type Subject struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// Resource is an AuthZEN resource.
type Resource struct {
	Type       string         `json:"type"`
	ID         string         `json:"id"`
	Properties map[string]any `json:"properties,omitempty"`
}

type actionBody struct {
	Name string `json:"name"`
}

type evaluationRequest struct {
	Subject  Subject    `json:"subject"`
	Action   actionBody `json:"action"`
	Resource Resource   `json:"resource"`
}

type evaluationResponse struct {
	Decision *bool `json:"decision"`
}

type batchItem struct {
	Resource Resource `json:"resource"`
}

type batchRequest struct {
	Subject     Subject     `json:"subject"`
	Action      actionBody  `json:"action"`
	Evaluations []batchItem `json:"evaluations"`
}

type batchResponse struct {
	Evaluations []evaluationResponse `json:"evaluations"`
}

// PDPConfig configures the AuthZEN client.
type PDPConfig struct {
	BaseURL  string
	Token    string
	Timeout  time.Duration
	CacheTTL time.Duration
	// HTTPClient is used for PDP requests; nil means a new client.
	HTTPClient *http.Client
	// Now returns the current time; nil means time.Now.
	Now func() time.Time
}

// PDPClient calls an OpenID AuthZEN Authorization API 1.0 PDP.
type PDPClient struct {
	cfg   PDPConfig
	cache *decisionCache
}

// NewPDPClient returns an AuthZEN client.
func NewPDPClient(cfg PDPConfig) *PDPClient {
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	c := &PDPClient{cfg: cfg}
	if cfg.CacheTTL > 0 {
		c.cache = &decisionCache{ttl: cfg.CacheTTL, now: cfg.Now, entries: map[string]cacheEntry{}}
	}
	return c
}

// Evaluate asks the PDP for one decision.
func (c *PDPClient) Evaluate(ctx context.Context, subject Subject, action Action, resource Resource) (bool, error) {
	key := cacheKey(subject, action, resource)
	if decision, ok := c.cache.get(key); ok {
		return decision, nil
	}

	var answer evaluationResponse
	req := evaluationRequest{Subject: subject, Action: actionBody{Name: string(action)}, Resource: resource}
	if err := c.post(ctx, evaluationPath, req, &answer); err != nil {
		return false, err
	}
	if answer.Decision == nil {
		return false, errors.New("PDP answer has no boolean decision")
	}
	c.cache.put(key, *answer.Decision)
	return *answer.Decision, nil
}

// EvaluateBatch asks the PDP for one decision per resource with the
// evaluations endpoint. The result has the same order as resources.
func (c *PDPClient) EvaluateBatch(ctx context.Context, subject Subject, action Action, resources []Resource) ([]bool, error) {
	decisions := make([]bool, len(resources))
	var pending []int
	for i, r := range resources {
		if decision, ok := c.cache.get(cacheKey(subject, action, r)); ok {
			decisions[i] = decision
			continue
		}
		pending = append(pending, i)
	}

	for start := 0; start < len(pending); start += maxBatchSize {
		chunk := pending[start:min(start+maxBatchSize, len(pending))]
		req := batchRequest{Subject: subject, Action: actionBody{Name: string(action)}}
		for _, i := range chunk {
			req.Evaluations = append(req.Evaluations, batchItem{Resource: resources[i]})
		}
		var answer batchResponse
		if err := c.post(ctx, evaluationsPath, req, &answer); err != nil {
			return nil, err
		}
		if len(answer.Evaluations) != len(chunk) {
			return nil, fmt.Errorf("PDP answered %d evaluations for %d requests", len(answer.Evaluations), len(chunk))
		}
		for j, i := range chunk {
			if answer.Evaluations[j].Decision == nil {
				return nil, fmt.Errorf("PDP evaluation %d has no boolean decision", j)
			}
			decisions[i] = *answer.Evaluations[j].Decision
			c.cache.put(cacheKey(subject, action, resources[i]), decisions[i])
		}
	}
	return decisions, nil
}

func (c *PDPClient) post(ctx context.Context, path string, body, answer any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	}

	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("PDP request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxPDPAnswerBytes))
		return fmt.Errorf("PDP answered HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxPDPAnswerBytes+1))
	if err != nil {
		return fmt.Errorf("read PDP answer: %w", err)
	}
	if len(data) > maxPDPAnswerBytes {
		return errors.New("PDP answer is too large")
	}
	if err := json.Unmarshal(data, answer); err != nil {
		return fmt.Errorf("decode PDP answer: %w", err)
	}
	return nil
}

func cacheKey(subject Subject, action Action, resource Resource) string {
	runID, _ := resource.Properties["run_id"].(string)
	return strings.Join([]string{subject.Type, subject.ID, string(action), resource.Type, resource.ID, runID}, "\x00")
}

type cacheEntry struct {
	decision bool
	expires  time.Time
}

// decisionCache keeps positive and negative decisions for a short time. A nil
// cache stores nothing.
type decisionCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	now     func() time.Time
	entries map[string]cacheEntry
}

func (c *decisionCache) get(key string) (bool, bool) {
	if c == nil {
		return false, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return false, false
	}
	if !c.now().Before(entry.expires) {
		delete(c.entries, key)
		return false, false
	}
	return entry.decision, true
}

func (c *decisionCache) put(key string, decision bool) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if len(c.entries) >= maxCacheEntries {
		for k, e := range c.entries {
			if !now.Before(e.expires) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= maxCacheEntries {
			clear(c.entries)
		}
	}
	c.entries[key] = cacheEntry{decision: decision, expires: now.Add(c.ttl)}
}
