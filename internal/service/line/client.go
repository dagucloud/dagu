// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package line

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultLineAPIBaseURL = "https://api.line.me"

type lineClientAPI interface {
	PushText(ctx context.Context, to, text string) error
	ReplyText(ctx context.Context, replyToken, text string) error
}

type httpLineClient struct {
	channelAccessToken string
	httpClient         *http.Client
	baseURL            string
}

func newHTTPLineClient(channelAccessToken string) *httpLineClient {
	return &httpLineClient{
		channelAccessToken: channelAccessToken,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL: defaultLineAPIBaseURL,
	}
}

func (c *httpLineClient) PushText(ctx context.Context, to, text string) error {
	body := linePushRequest{
		To: to,
		Messages: []lineOutboundMessage{{
			Type: "text",
			Text: text,
		}},
	}
	return c.postJSON(ctx, "/v2/bot/message/push", body)
}

func (c *httpLineClient) ReplyText(ctx context.Context, replyToken, text string) error {
	body := lineReplyRequest{
		ReplyToken: replyToken,
		Messages: []lineOutboundMessage{{
			Type: "text",
			Text: text,
		}},
	}
	return c.postJSON(ctx, "/v2/bot/message/reply", body)
}

func (c *httpLineClient) postJSON(ctx context.Context, endpoint string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal LINE request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.baseURL, "/")+endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create LINE request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.channelAccessToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send LINE request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		return nil
	}

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if len(respBody) == 0 {
		return fmt.Errorf("LINE API returned status %d", resp.StatusCode)
	}
	return fmt.Errorf("LINE API returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
}

type linePushRequest struct {
	To       string                `json:"to"`
	Messages []lineOutboundMessage `json:"messages"`
}

type lineReplyRequest struct {
	ReplyToken string                `json:"replyToken"`
	Messages   []lineOutboundMessage `json:"messages"`
}

type lineOutboundMessage struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
