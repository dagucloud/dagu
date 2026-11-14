// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ssh

import (
	"crypto/sha256"
	"sync"

	"golang.org/x/crypto/ssh"
)

// signerCache keeps the private keys one run has parsed, keyed by a digest of
// the key text and passphrase. Decrypting a passphrase-protected key runs a
// deliberately slow key derivation, so a run that connects many times parses
// each key once. Concurrent requests for the same key wait for one parse.
type signerCache struct {
	mu      sync.Mutex
	entries map[[sha256.Size]byte]*signerEntry
}

type signerEntry struct {
	once   sync.Once
	signer ssh.Signer
	err    error
}

// signer returns the parsed key for pem and passphrase, calling parse only for
// the first request of each pair.
func (c *signerCache) signer(pem []byte, passphrase string, parse func([]byte, string) (ssh.Signer, error)) (ssh.Signer, error) {
	h := sha256.New()
	_, _ = h.Write(pem)
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(passphrase))
	var key [sha256.Size]byte
	h.Sum(key[:0])

	c.mu.Lock()
	if c.entries == nil {
		c.entries = make(map[[sha256.Size]byte]*signerEntry)
	}
	entry, ok := c.entries[key]
	if !ok {
		entry = &signerEntry{}
		c.entries[key] = entry
	}
	c.mu.Unlock()

	entry.once.Do(func() {
		entry.signer, entry.err = parse(pem, passphrase)
	})
	return entry.signer, entry.err
}
