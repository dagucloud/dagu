// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sock

import (
	"crypto/md5" //nolint:gosec
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAddr(t *testing.T) {
	t.Parallel()

	dataDir := filepath.Join(t.TempDir(), "data")

	t.Run("Deterministic", func(t *testing.T) {
		t.Parallel()
		addr := Addr(dataDir, "mydag", "run123")
		require.Equal(t, addr, Addr(dataDir, "mydag", "run123"))
		require.NotEqual(t, addr, Addr(dataDir, "mydag", "run456"))
		require.True(t, strings.HasPrefix(addr, filepath.Join(socketDir(), "@dagu_mydag_")))
		require.True(t, strings.HasSuffix(addr, ".sock"))
	})

	// The processes of one install can spell its data directory in more
	// than one way; the processes of two installs never share an address.
	t.Run("OneAddressForEachInstall", func(t *testing.T) {
		t.Parallel()
		addr := Addr(dataDir, "mydag", "run123")
		require.Equal(t, addr, Addr(dataDir+string(filepath.Separator), "mydag", "run123"))
		sep := string(filepath.Separator)
		require.Equal(t, addr, Addr(dataDir+sep+".."+sep+"data", "mydag", "run123"))
		require.NotEqual(t, addr, Addr(filepath.Join(t.TempDir(), "data"), "mydag", "run123"))
		require.NotEqual(t, addr, LegacyAddr("mydag", "run123"))
	})

	// Two processes of one install can reach its data directory through a
	// symlink and through its target.
	t.Run("SymlinkResolves", func(t *testing.T) {
		t.Parallel()
		if runtime.GOOS == "windows" {
			t.Skip("creating a symlink needs a privilege on Windows")
		}
		target := filepath.Join(t.TempDir(), "data")
		require.NoError(t, os.MkdirAll(target, 0o750))
		link := filepath.Join(t.TempDir(), "current")
		require.NoError(t, os.Symlink(target, link))
		require.Equal(t, Addr(target, "mydag", "run123"), Addr(link, "mydag", "run123"))
	})

	// LegacyAddr keeps the bytes earlier versions used, so a run they
	// started stays reachable. The socket name embeds the identity, so even
	// a Windows device name such as con is no device there.
	t.Run("DeviceNameUnchanged", func(t *testing.T) {
		t.Parallel()
		hash := fmt.Sprintf("%x", md5.Sum([]byte("con"+"run123")))[:6] //nolint:gosec
		require.Equal(t, filepath.Join(socketDir(), "@dagu_mydag_c2a8cc.sock"), LegacyAddr("mydag", "run123"))
		require.Equal(t, "@dagu_con_"+hash+".sock", filepath.Base(LegacyAddr("con", "run123")))
		require.True(t, strings.HasPrefix(filepath.Base(Addr(dataDir, "con", "run123")), "@dagu_con_"))
	})

	t.Run("SafeAndPortableLength", func(t *testing.T) {
		t.Parallel()
		identity := "my/dag\\with:special*chars" + strings.Repeat("x", 100)
		longDataDir := filepath.Join(dataDir, strings.Repeat("d", 200))
		for _, addr := range []string{
			Addr(longDataDir, identity, "run|with<>chars"),
			LegacyAddr(identity, "run|with<>chars"),
		} {
			name := filepath.Base(addr)
			require.LessOrEqual(t, len(name), maxSocketNameLength)
			for _, char := range []string{"/", "\\", ":", "*", "|", "<", ">"} {
				require.NotContains(t, name, char)
			}
		}
	})
}
