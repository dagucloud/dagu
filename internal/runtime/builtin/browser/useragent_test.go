// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"os"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The user agent a browser sends with a window is the one it sends without,
// less the headless mark, whatever else the browser adds.
func TestHeadedFrom(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct{ sent, headed string }{
		"chrome": {
			sent:   "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) HeadlessChrome/155.0.0.0 Safari/537.36",
			headed: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/155.0.0.0 Safari/537.36",
		},
		"edge": {
			sent:   "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) HeadlessChrome/155.0.0.0 Safari/537.36 Edg/155.0.0.0",
			headed: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/155.0.0.0 Safari/537.36 Edg/155.0.0.0",
		},
		"with a window": {
			sent:   "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/155.0.0.0 Safari/537.36",
			headed: "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/155.0.0.0 Safari/537.36",
		},
	} {
		agent := headedFrom("/opt/chrome", browserhost.Version{Product: "Chrome/155.0.8059.80", UserAgent: tc.sent})
		assert.Equal(t, headedUserAgent{Executable: "/opt/chrome", Product: "Chrome/155.0.8059.80", UserAgent: tc.headed}, agent, name)
	}
}

// A user agent is kept per executable, and one that cannot be read is
// learned again.
func TestKeptUserAgents(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	chrome := headedUserAgent{Executable: "/opt/chrome", Product: "Chrome/155.0.8059.80", UserAgent: "Mozilla/5.0 Chrome/155.0.0.0"}
	require.NoError(t, keepUserAgent(dir, chrome))
	kept, ok := loadUserAgent(dir, "/opt/chrome")
	require.True(t, ok)
	assert.Equal(t, chrome, kept)
	_, ok = loadUserAgent(dir, "/opt/edge")
	assert.False(t, ok, "another executable has its own")

	require.NoError(t, os.WriteFile(userAgentPath(dir, "/opt/chrome"), []byte("{"), userAgentFileMode))
	_, ok = loadUserAgent(dir, "/opt/chrome")
	assert.False(t, ok)
}

// A launch that names no executable starts the one CHROME_PATH names, so
// its user agent is kept under that name.
func TestUserAgentKeyFollowsChromePath(t *testing.T) {
	t.Setenv(chromePathEnv, "/opt/chrome")

	assert.Equal(t, "/opt/chrome", userAgentKey(""))
	assert.Equal(t, "/opt/edge", userAgentKey("/opt/edge"))
}
