// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package webhooksig

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The examples each sender publishes verify, read as the relay hands headers
// on: one value each, under Go's canonical names.
func TestVerifyPublishedExamples(t *testing.T) {
	slackBody := "token=xyzz0WbapA4vBCDEFasx0q6G&team_id=T1DC2JH3J&team_domain=testteamnow&channel_id=G8PSS9T3V&channel_name=foobar&user_id=U2CERLKJA&user_name=roadrunner&command=%2Fwebhook-collect&text=&response_url=https%3A%2F%2Fhooks.slack.com%2Fcommands%2FT1DC2JH3J%2F397700885554%2F96rGlfmibIGlgcZRskXaIFfN&trigger_id=398738663015.47445629121.803a0bc887a14d10d2c447fce8b6703c"
	for _, tc := range []struct {
		sender   Sender
		secret   string
		header   http.Header
		body     string
		received time.Time
	}{
		{GitHub, "It's a Secret to Everybody", http.Header{"X-Hub-Signature-256": {"sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17"}}, "Hello, World!", time.Now()},
		{Slack, "8f742231b10e8888abcd99yyyzzz85a5", http.Header{
			"X-Slack-Request-Timestamp": {"1531420618"},
			"X-Slack-Signature":         {"v0=a2114d57b48eac39b9ad189dd8316235a7b4a8d21a10bd27519666489c69b503"},
		}, slackBody, time.Unix(1531420618, 0).Add(time.Minute)},
		{Standard, "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw", http.Header{
			"Webhook-Id":        {"msg_p5jXN8AQM9LWM0D4loKWxJek"},
			"Webhook-Timestamp": {"1614265330"},
			"Webhook-Signature": {"v1,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE="},
		}, `{"test": 2432232314}`, time.Unix(1614265330, 0)},
	} {
		assert.NoError(t, Verify(tc.sender, tc.secret, tc.header, []byte(tc.body), tc.received), tc.sender)
	}
}

// What Sign makes, Verify accepts; a changed body, another secret, a missing
// header, or a timestamp far from arrival is refused.
func TestSignAndVerify(t *testing.T) {
	secrets := map[Sender]string{
		GitHub:   "a-github-secret",
		Stripe:   "whsec_stripe-test-secret",
		Slack:    "8f742231b10e8888abcd99yyyzzz85a5",
		Standard: "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw",
	}
	others := map[Sender]string{
		GitHub:   "another-secret",
		Stripe:   "whsec_another",
		Slack:    "0000000000000000000000000000000a",
		Standard: "whsec_" + strings.Repeat("A", 32),
	}
	body := []byte(`{"type":"invoice.paid"}`)
	now := time.Unix(1_760_000_000, 0)
	for _, sender := range Senders {
		header, err := Sign(sender, secrets[sender], body, now)
		require.NoError(t, err, sender)
		assert.NoError(t, Verify(sender, secrets[sender], header, body, now.Add(time.Minute)), "%s: a signed request", sender)
		assert.ErrorIs(t, Verify(sender, secrets[sender], header, []byte(`{"type":"invoice.voided"}`), now), ErrMismatch, "%s: a changed body", sender)
		assert.ErrorIs(t, Verify(sender, others[sender], header, body, now), ErrMismatch, "%s: another secret", sender)
		assert.Error(t, Verify(sender, secrets[sender], http.Header{}, body, now), "%s: an unsigned request", sender)
		late := Verify(sender, secrets[sender], header, body, now.Add(10*time.Minute))
		if sender == GitHub {
			assert.NoError(t, late, "GitHub signs no timestamp")
		} else {
			assert.ErrorIs(t, late, ErrStale, "%s: a request arriving 10 minutes after it was signed", sender)
		}
	}
}

// A request signed with each of a sender's secrets in use while one replaces
// another verifies with either.
func TestVerifyEitherSignature(t *testing.T) {
	body := []byte(`{}`)
	now := time.Unix(1_760_000_000, 0)
	old, err := Sign(Stripe, "whsec_old", body, now)
	require.NoError(t, err)
	current, err := Sign(Stripe, "whsec_current", body, now)
	require.NoError(t, err)
	_, v1, _ := strings.Cut(current.Get("Stripe-Signature"), ",")
	both := http.Header{"Stripe-Signature": {old.Get("Stripe-Signature") + "," + v1}}
	for _, secret := range []string{"whsec_old", "whsec_current"} {
		assert.NoError(t, Verify(Stripe, secret, both, body, now), secret)
	}

	oldKey, currentKey := "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw", "whsec_"+strings.Repeat("B", 32)
	header, err := Sign(Standard, oldKey, body, now)
	require.NoError(t, err)
	key, err := StandardKey(currentKey)
	require.NoError(t, err)
	header.Set("Webhook-Signature", header.Get("Webhook-Signature")+" "+StandardSignature(key, header.Get("Webhook-Id"), now.Unix(), body))
	for _, secret := range []string{oldKey, currentKey} {
		assert.NoError(t, Verify(Standard, secret, header, body, now), secret)
	}
}

func TestCheckSecret(t *testing.T) {
	for _, tc := range []struct {
		sender Sender
		secret string
		valid  bool
	}{
		{GitHub, "anything", true},
		{GitHub, " ", false},
		{Slack, "8f742231b10e8888abcd99yyyzzz85a5", true},
		{Stripe, "whsec_abc", true},
		{Stripe, "sk_live_abc", false},
		{Standard, "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw", true},
		{Standard, "whsec_short", false},
		{"zapier", "anything", false},
	} {
		err := CheckSecret(tc.sender, tc.secret)
		assert.Equal(t, tc.valid, err == nil, "%s %q: %v", tc.sender, tc.secret, err)
	}
}

func TestParse(t *testing.T) {
	sender, err := Parse("stripe")
	require.NoError(t, err)
	assert.Equal(t, Stripe, sender)
	_, err = Parse("zapier")
	assert.Error(t, err)
}

// A new secret signs and verifies as GitHub's.
func TestNewSecret(t *testing.T) {
	secret := NewSecret()
	assert.Len(t, secret, 64)
	assert.NotEqual(t, secret, NewSecret())
	header, err := Sign(GitHub, secret, []byte(`{}`), time.Now())
	require.NoError(t, err)
	assert.NoError(t, Verify(GitHub, secret, header, []byte(`{}`), time.Now()))
}
