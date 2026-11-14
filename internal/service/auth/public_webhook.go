// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/webhooksig"
)

var (
	ErrWebhookPublicNotSupported    = errors.New("public webhook URLs need encrypted storage, which is not available")
	ErrWebhookPublicNotEnabled      = errors.New("webhook has no public URL")
	ErrInvalidPublicWebhookSettings = errors.New("invalid public webhook settings")
)

// publicWebhookTokenBytes is the size of the secret a public URL ends in.
const publicWebhookTokenBytes = 32

// PublicWebhookSenderInput is who may send to a public URL and what starts
// runs.
type PublicWebhookSenderInput struct {
	// Sender names whose signature requests must carry, as webhooksig names
	// it; empty accepts anyone with the URL.
	Sender string
	// Secret is the sender's signing secret. Empty keeps the current secret
	// of the same sender, or makes one for GitHub.
	Secret string
	// GenerateSecret makes a new GitHub secret.
	GenerateSecret bool
	// Events names the events that start runs; empty takes every event.
	Events []string
}

// PublicWebhookSenderResult holds the webhook and the secret made for it,
// which is returned only this once.
type PublicWebhookSenderResult struct {
	Webhook         *auth.Webhook
	GeneratedSecret string
}

// EnablePublicWebhook gives the webhook a public URL. A webhook that has one
// keeps it.
func (s *Service) EnablePublicWebhook(ctx context.Context, dagName string) (*auth.Webhook, error) {
	if s.webhookStore == nil {
		return nil, ErrWebhookNotConfigured
	}
	token, err := generatePublicWebhookToken()
	if err != nil {
		return nil, err
	}
	return s.updatePublicWebhook(ctx, dagName, func(webhook *auth.Webhook) error {
		if webhook.Public == nil {
			webhook.Public = &auth.WebhookPublic{Token: token, CreatedAt: time.Now().UTC()}
		}
		return nil
	})
}

// DisablePublicWebhook removes the webhook's public URL.
func (s *Service) DisablePublicWebhook(ctx context.Context, dagName string) (*auth.Webhook, error) {
	if s.webhookStore == nil {
		return nil, ErrWebhookNotConfigured
	}
	return s.updatePublicWebhook(ctx, dagName, func(webhook *auth.Webhook) error {
		webhook.Public = nil
		return nil
	})
}

// RegeneratePublicWebhookToken replaces the webhook's public URL; the old one
// stops working.
func (s *Service) RegeneratePublicWebhookToken(ctx context.Context, dagName string) (*auth.Webhook, error) {
	if s.webhookStore == nil {
		return nil, ErrWebhookNotConfigured
	}
	token, err := generatePublicWebhookToken()
	if err != nil {
		return nil, err
	}
	return s.updatePublicWebhook(ctx, dagName, func(webhook *auth.Webhook) error {
		if webhook.Public == nil {
			return ErrWebhookPublicNotEnabled
		}
		webhook.Public.Token = token
		return nil
	})
}

// ConfigurePublicWebhookSender sets who may send to the webhook's public URL
// and which events start runs.
func (s *Service) ConfigurePublicWebhookSender(
	ctx context.Context,
	dagName string,
	input PublicWebhookSenderInput,
) (*PublicWebhookSenderResult, error) {
	if s.webhookStore == nil {
		return nil, ErrWebhookNotConfigured
	}
	events, err := webhooksig.Events(input.Events)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidPublicWebhookSettings, err)
	}
	var sender webhooksig.Sender
	if input.Sender != "" {
		if sender, err = webhooksig.Parse(input.Sender); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidPublicWebhookSettings, err)
		}
	}
	secret := strings.TrimSpace(input.Secret)
	switch {
	case sender == "" && (secret != "" || input.GenerateSecret):
		return nil, fmt.Errorf("%w: a URL anyone may send to takes no signing secret", ErrInvalidPublicWebhookSettings)
	case input.GenerateSecret && sender != webhooksig.GitHub:
		return nil, fmt.Errorf("%w: only a GitHub signing secret can be generated", ErrInvalidPublicWebhookSettings)
	case input.GenerateSecret && secret != "":
		return nil, fmt.Errorf("%w: enter a signing secret or generate one, not both", ErrInvalidPublicWebhookSettings)
	}
	newSecret := webhooksig.NewSecret()

	var generated string
	webhook, err := s.updatePublicWebhook(ctx, dagName, func(webhook *auth.Webhook) error {
		public := webhook.Public
		if public == nil {
			return ErrWebhookPublicNotEnabled
		}
		chosen := secret
		generated = ""
		if sender != "" && chosen == "" {
			switch {
			case input.GenerateSecret:
				chosen, generated = newSecret, newSecret
			case public.Sender == string(sender) && public.SenderSecret != "":
				chosen = public.SenderSecret
			case sender == webhooksig.GitHub:
				chosen, generated = newSecret, newSecret
			}
		}
		if sender != "" {
			if err := webhooksig.CheckSecret(sender, chosen); err != nil {
				return fmt.Errorf("%w: %w", ErrInvalidPublicWebhookSettings, err)
			}
		}
		public.Sender = string(sender)
		public.SenderSecret = chosen
		public.Events = events
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &PublicWebhookSenderResult{Webhook: webhook, GeneratedSecret: generated}, nil
}

func (s *Service) updatePublicWebhook(ctx context.Context, dagName string, mutate func(*auth.Webhook) error) (*auth.Webhook, error) {
	webhook, err := s.updateWebhook(ctx, dagName, mutate)
	if errors.Is(err, auth.ErrWebhookPublicEncryptorRequired) {
		return nil, ErrWebhookPublicNotSupported
	}
	return webhook, err
}

func generatePublicWebhookToken() (string, error) {
	b := make([]byte, publicWebhookTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate public webhook token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
