// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package gogitssh

import (
	"fmt"
	"os"

	"github.com/go-git/go-git/v5/plumbing/transport"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"golang.org/x/crypto/ssh"
)

var _ gitssh.AuthMethod = (*publicKeysAuth)(nil)

// NewPublicKeysFromFile returns go-git SSH auth for a private key file.
func NewPublicKeysFromFile(user, pemFile, passphrase string) (gitssh.AuthMethod, error) {
	pemBytes, err := os.ReadFile(pemFile)
	if err != nil {
		return nil, err
	}
	return NewPublicKeys(user, pemBytes, passphrase)
}

// NewPublicKeys returns go-git SSH auth for a private key.
func NewPublicKeys(user string, pemBytes []byte, passphrase string) (gitssh.AuthMethod, error) {
	signer, err := ssh.ParsePrivateKey(pemBytes)
	if _, ok := err.(*ssh.PassphraseMissingError); ok {
		signer, err = ssh.ParsePrivateKeyWithPassphrase(pemBytes, []byte(passphrase))
	}
	if err != nil {
		return nil, err
	}
	return &publicKeysAuth{user: user, signer: signer}, nil
}

// UserFromURL returns the SSH username embedded in repoURL, or fallback.
func UserFromURL(repoURL, fallback string) string {
	endpoint, err := transport.NewEndpoint(repoURL)
	if err != nil || endpoint.User == "" {
		return fallback
	}
	return endpoint.User
}

type publicKeysAuth struct {
	user   string
	signer ssh.Signer
}

func (a *publicKeysAuth) Name() string {
	return gitssh.PublicKeysName
}

func (a *publicKeysAuth) String() string {
	return fmt.Sprintf("user: %s, name: %s", a.user, a.Name())
}

func (a *publicKeysAuth) ClientConfig() (*ssh.ClientConfig, error) {
	return &ssh.ClientConfig{
		User: a.user,
		Auth: []ssh.AuthMethod{ssh.PublicKeys(a.signer)},
	}, nil
}
