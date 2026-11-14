// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ssh

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

func TestSignerCacheParsesEachKeyOnce(t *testing.T) {
	t.Parallel()

	key := newTestKey(t)
	var parses atomic.Int32
	parse := func(pem []byte, passphrase string) (gossh.Signer, error) {
		parses.Add(1)
		return parseSigner(pem, passphrase)
	}
	var cache signerCache

	// Concurrent first requests for one key wait for a single parse.
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			signer, err := cache.signer([]byte(key.encrypted), testPassphrase, parse)
			assert.NoError(t, err)
			assert.Equal(t, key.public.Marshal(), signer.PublicKey().Marshal())
		})
	}
	wg.Wait()
	assert.Equal(t, int32(1), parses.Load())

	// Another passphrase is another entry, and its failure is kept too.
	for range 2 {
		_, err := cache.signer([]byte(key.encrypted), "wrong", parse)
		require.Error(t, err)
	}
	assert.Equal(t, int32(2), parses.Load())
}

// Connections in one run share parsed keys, so an encrypted key is decrypted
// once however many steps or foreach items connect with it.
func TestKeySignerCachesWithinRun(t *testing.T) {
	t.Parallel()

	key := newTestKey(t)
	values := map[string]string{"ssh/key": key.encrypted, "ssh/passphrase": testPassphrase}
	auth := ir.SSHAuth{KeySecret: "ssh/key", PassphraseSecret: "ssh/passphrase"}

	run := WithSecretValues(context.Background(), values)
	for range 3 {
		_, err := keySigner(run, auth, "")
		require.NoError(t, err)
	}
	// keySigner reports no parse count, so the cache entries show that the
	// three connections shared one parse.
	assert.Len(t, credentialsFromContext(run).signers.entries, 1)

	// Another run starts with an empty cache.
	other := WithSecretValues(context.Background(), values)
	assert.Empty(t, credentialsFromContext(other).signers.entries)
}
