// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strconv"

	"github.com/dagucloud/dagu/v2/internal/cmn/replaycache"
)

// replayCache stores the actions each act operation performed, so later runs
// repeat them without a model call. Entries are keyed by operation position,
// instruction, and page, so an edited instruction or a different page misses.
type replayCache = replaycache.Recordings[[]recordedAction]

func openReplayCache(browserDir, dagName, stepKey string) (*replayCache, error) {
	return replaycache.Open[[]recordedAction](replaycache.New(browserDir).Path(dagName, stepKey))
}

// replayKey identifies an act operation on a page. Query strings and
// fragments are ignored so pagination or tracking parameters still hit.
func replayKey(index int, instruction, pageURL string) string {
	page := pageURL
	if parsed, err := url.Parse(pageURL); err == nil {
		parsed.RawQuery = ""
		parsed.Fragment = ""
		page = parsed.String()
	}
	sum := sha256.Sum256([]byte(strconv.Itoa(index) + "\x00" + instruction + "\x00" + page))
	return hex.EncodeToString(sum[:])
}
