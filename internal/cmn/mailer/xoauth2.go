// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mailer

import (
	"errors"
	"fmt"
	"net/smtp"
)

type xoauth2Auth struct {
	username  string
	token     string
	challenge string
}

func (a *xoauth2Auth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS {
		return "", nil, errors.New("XOAUTH2 requires a TLS connection")
	}
	response := fmt.Sprintf("user=%s\x01auth=Bearer %s\x01\x01", a.username, a.token)
	return "XOAUTH2", []byte(response), nil
}

func (a *xoauth2Auth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	a.challenge = string(fromServer)
	// A non-nil response keeps net/smtp reading the server's final authentication error.
	return []byte{}, nil
}
