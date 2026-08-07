// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mailer

import (
	"fmt"
	"strings"

	mailoauth "github.com/dagucloud/dagu/v2/internal/cmn/mailer/oauth"
)

// BuildConfig constructs a mailer configuration from resolved SMTP settings.
func BuildConfig(host, port, username, password string, oauthConfig *mailoauth.Config) (Config, error) {
	if oauthConfig == nil {
		return Config{Host: host, Port: port, Username: username, Password: password}, nil
	}

	host = strings.TrimSpace(host)
	port = strings.TrimSpace(port)
	username = strings.TrimSpace(username)
	if strings.TrimSpace(password) != "" {
		return Config{}, fmt.Errorf("SMTP password and OAuth are mutually exclusive")
	}
	oauthConfigCopy := *oauthConfig
	oauthConfigCopy.Provider = strings.TrimSpace(oauthConfigCopy.Provider)
	destination, err := mailoauth.SMTPDestination(oauthConfigCopy.Provider)
	if err != nil {
		return Config{}, err
	}
	if host != "" && host != destination.Host {
		return Config{}, fmt.Errorf("SMTP OAuth provider %q requires host %q", oauthConfigCopy.Provider, destination.Host)
	}
	if port != "" && port != destination.Port {
		return Config{}, fmt.Errorf("SMTP OAuth provider %q requires port %q", oauthConfigCopy.Provider, destination.Port)
	}
	token, err := mailoauth.NewTokenFunc(username, &oauthConfigCopy)
	if err != nil {
		return Config{}, err
	}
	return Config{
		Host:     destination.Host,
		Port:     destination.Port,
		Username: username,
		Token:    token,
	}, nil
}
