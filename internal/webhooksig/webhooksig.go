// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package webhooksig signs and verifies webhook requests the way the services
// that send them do: GitHub, Stripe, Slack, and senders that follow Standard
// Webhooks.
package webhooksig

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Sender names a format of signed webhook requests.
type Sender string

const (
	GitHub   Sender = "github"
	Stripe   Sender = "stripe"
	Slack    Sender = "slack"
	Standard Sender = "standard"
)

// Senders lists every format, in the order a person chooses among them.
var Senders = []Sender{GitHub, Stripe, Slack, Standard}

// tolerance is how far a signed timestamp may be from when the request
// arrived, as Stripe, Slack, and Standard Webhooks recommend.
const tolerance = 5 * time.Minute

const standardSecretPrefix = "whsec_"

var (
	ErrMismatch = errors.New("the signature does not match the signing secret")
	ErrStale    = errors.New("the signature was made more than 5 minutes before or after the request arrived")
)

// Valid reports whether s names a format this package knows.
func (s Sender) Valid() bool {
	return slices.Contains(Senders, s)
}

// Parse returns the sender name names, or an error for a name this package
// does not know.
func Parse(name string) (Sender, error) {
	if s := Sender(name); s.Valid() {
		return s, nil
	}
	return "", fmt.Errorf("unknown sender %q", name)
}

// CheckSecret reports whether secret has the form the sender's signing
// secrets take.
func CheckSecret(s Sender, secret string) error {
	switch s {
	case GitHub, Slack:
		if strings.TrimSpace(secret) == "" {
			return errors.New("enter the signing secret")
		}
		return nil
	case Stripe:
		if !strings.HasPrefix(secret, standardSecretPrefix) {
			return errors.New("a Stripe signing secret starts with whsec_")
		}
		return nil
	case Standard:
		_, err := StandardKey(secret)
		return err
	}
	return fmt.Errorf("unknown sender %q", s)
}

// Verify checks that a request carries a valid signature by the sender over
// body, made with secret within 5 minutes of received.
func Verify(s Sender, secret string, header http.Header, body []byte, received time.Time) error {
	switch s {
	case GitHub:
		signature, err := required(header, "X-Hub-Signature-256")
		if err != nil {
			return err
		}
		return match(hexSignature(secret, "sha256=", body), signature)
	case Stripe:
		fields, err := required(header, "Stripe-Signature")
		if err != nil {
			return err
		}
		timestamp, signatures := stripeFields(fields)
		if err := fresh(timestamp, received); err != nil {
			return err
		}
		want := hexSignature(secret, "", []byte(timestamp+"."), body)
		return match(want, signatures...)
	case Slack:
		signature, err := required(header, "X-Slack-Signature")
		if err != nil {
			return err
		}
		timestamp, err := required(header, "X-Slack-Request-Timestamp")
		if err != nil {
			return err
		}
		if err := fresh(timestamp, received); err != nil {
			return err
		}
		return match(hexSignature(secret, "v0=", []byte("v0:"+timestamp+":"), body), signature)
	case Standard:
		key, err := StandardKey(secret)
		if err != nil {
			return err
		}
		var values [3]string
		for i, name := range []string{"Webhook-Id", "Webhook-Timestamp", "Webhook-Signature"} {
			if values[i], err = required(header, name); err != nil {
				return err
			}
		}
		id, timestamp, signatures := values[0], values[1], strings.Fields(values[2])
		if err := fresh(timestamp, received); err != nil {
			return err
		}
		unix, _ := strconv.ParseInt(timestamp, 10, 64)
		return match(StandardSignature(key, id, unix, body), signatures...)
	}
	return fmt.Errorf("unknown sender %q", s)
}

// Sign returns the headers that carry the sender's signature over body,
// made with secret at now.
func Sign(s Sender, secret string, body []byte, now time.Time) (http.Header, error) {
	if err := CheckSecret(s, secret); err != nil {
		return nil, err
	}
	timestamp := strconv.FormatInt(now.Unix(), 10)
	switch s {
	case GitHub:
		return http.Header{"X-Hub-Signature-256": {hexSignature(secret, "sha256=", body)}}, nil
	case Stripe:
		return http.Header{"Stripe-Signature": {"t=" + timestamp + ",v1=" + hexSignature(secret, "", []byte(timestamp+"."), body)}}, nil
	case Slack:
		return http.Header{
			"X-Slack-Request-Timestamp": {timestamp},
			"X-Slack-Signature":         {hexSignature(secret, "v0=", []byte("v0:"+timestamp+":"), body)},
		}, nil
	case Standard:
		key, _ := StandardKey(secret)
		id := "msg_" + randomID()
		return http.Header{
			"Webhook-Id":        {id},
			"Webhook-Timestamp": {timestamp},
			"Webhook-Signature": {StandardSignature(key, id, now.Unix(), body)},
		}, nil
	}
	return nil, fmt.Errorf("unknown sender %q", s)
}

// StandardKey is the key a Standard Webhooks signing secret holds: whsec_
// followed by a base64 key of 24 to 64 bytes.
func StandardKey(secret string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, standardSecretPrefix))
	if err != nil || !strings.HasPrefix(secret, standardSecretPrefix) || len(key) < 24 || len(key) > 64 {
		return nil, errors.New("a webhook signing secret starts with whsec_ followed by a base64 key")
	}
	return key, nil
}

// StandardSignature signs one message as Standard Webhooks specifies: an
// HMAC-SHA256 over the message identifier, the timestamp, and the body.
func StandardSignature(key []byte, id string, timestamp int64, body []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id + "." + strconv.FormatInt(timestamp, 10) + "."))
	mac.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// hexSignature is prefix and the hex HMAC-SHA256, keyed by secret, of the
// parts in order.
func hexSignature(secret, prefix string, parts ...[]byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	for _, part := range parts {
		mac.Write(part)
	}
	return prefix + hex.EncodeToString(mac.Sum(nil))
}

// match reports whether any of the signatures a request carries is want.
func match(want string, signatures ...string) error {
	for _, signature := range signatures {
		if hmac.Equal([]byte(signature), []byte(want)) {
			return nil
		}
	}
	return ErrMismatch
}

func required(header http.Header, name string) (string, error) {
	value := strings.TrimSpace(header.Get(name))
	if value == "" {
		return "", fmt.Errorf("the request has no %s header", name)
	}
	return value, nil
}

// fresh reports whether a Unix timestamp in seconds is within tolerance of
// received.
func fresh(timestamp string, received time.Time) error {
	unix, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return ErrStale
	}
	if age := received.Sub(time.Unix(unix, 0)); age > tolerance || age < -tolerance {
		return ErrStale
	}
	return nil
}

// stripeFields splits a Stripe-Signature header into its timestamp and its
// v1 signatures, of which there is one per signing secret in use.
func stripeFields(value string) (timestamp string, signatures []string) {
	for field := range strings.SplitSeq(value, ",") {
		name, text, _ := strings.Cut(strings.TrimSpace(field), "=")
		switch name {
		case "t":
			timestamp = text
		case "v1":
			signatures = append(signatures, text)
		}
	}
	return timestamp, signatures
}

// NewSecret returns a random signing secret for a sender, such as GitHub,
// that takes whatever secret it is given.
func NewSecret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func randomID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
