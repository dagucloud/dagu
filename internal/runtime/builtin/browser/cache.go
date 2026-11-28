// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/replaycache"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
)

// replayCache stores what each act or upload operation did, so later runs
// repeat it without a model call. Entries are keyed by operation position,
// instruction, and page, so an edited instruction or a different page misses.
type replayCache = replaycache.Recordings[recording]

func openReplayCache(browserDir, dagName, stepKey string) *replayCache {
	return replaycache.OpenStep[recording](replaycache.New(browserDir), dagName, stepKey)
}

// recording is what an act or an upload did on a page.
type recording struct {
	Actions []recordedAction `json:"actions"`
	// Assumed holds the values of the variables an act names that its
	// actions never use, such as a field that already showed the value, so
	// the recording replays only where they still hold; see unfit.
	Assumed *agentstep.Assumed `json:"assumed,omitempty"`
}

// UnmarshalJSON reads a recording, or the bare list of actions an older
// engine kept for one, which notes nothing its actions assumed.
func (r *recording) UnmarshalJSON(data []byte) error {
	if trimmed := bytes.TrimSpace(data); len(trimmed) > 0 && trimmed[0] == '[' {
		*r = recording{}
		return json.Unmarshal(trimmed, &r.Actions)
	}
	type plain recording
	return json.Unmarshal(data, (*plain)(r))
}

// recordAct is the recording of an act's actions, run with values, which
// keeps what they took for granted about the variables instruction names.
func recordAct(instruction string, actions []recordedAction, values map[string]string) recording {
	return recording{Actions: actions, Assumed: agentstep.Assume(unusedVariables(instruction, actions), values)}
}

// unusedVariables returns the variables an act's instruction names that its
// actions never use: no argument holds the variable's placeholder, nor does
// the name, the column header or the text around an element a replay finds
// it by.
func unusedVariables(instruction string, actions []recordedAction) []string {
	var texts []string
	for _, action := range actions {
		texts = append(texts, action.Arguments...)
		if action.Target != nil {
			texts = append(texts, action.Target.Name, action.Target.Column)
			for _, name := range action.Target.ContextVariables {
				texts = append(texts, "%"+name+"%")
			}
		}
	}
	return agentstep.UnusedVariables(instruction, texts)
}

// unfit names a variable an act's instruction names that its recording
// never uses and whose value in values is not the one it was recorded with,
// or returns "" when the recording fits. Such a recording was made where
// the page already showed the value, so a replay with another value would
// leave the page wrong.
func unfit(instruction string, rec recording, values map[string]string) string {
	return rec.Assumed.Unmet(unusedVariables(instruction, rec.Actions), values)
}

// replayKey identifies an act operation on a page in the replay cache.
// Query strings and fragments are ignored so pagination or tracking
// parameters still hit, and path segments that look like record IDs are
// ignored so the same page of another record, such as /orders/4812/edit and
// /orders/4813/edit, hits. A page without record IDs keys as literalKey does.
func replayKey(index int, instruction, pageURL string) string {
	return pageKey(index, instruction, pageURL, true)
}

// literalKey identifies an act operation on the page it ran on, record IDs
// included. Commit points are keyed by it, so approving a write on one
// record's page approves it on no other, and it finds a recording kept
// before replayKey ignored record IDs.
func literalKey(index int, instruction, pageURL string) string {
	return pageKey(index, instruction, pageURL, false)
}

func pageKey(index int, instruction, pageURL string, ignoreIDs bool) string {
	page := pageURL
	if parsed, err := url.Parse(pageURL); err == nil {
		parsed.RawQuery = ""
		parsed.Fragment = ""
		segments, changed := strings.Split(parsed.Path, "/"), false
		for i, segment := range segments {
			if ignoreIDs && isRecordID(segment) {
				segments[i], changed = recordIDSegment, true
			}
		}
		if changed {
			parsed.Path, parsed.RawPath = strings.Join(segments, "/"), ""
		}
		page = parsed.String()
	}
	sum := sha256.Sum256([]byte(strconv.Itoa(index) + "\x00" + instruction + "\x00" + page))
	return hex.EncodeToString(sum[:])
}

// recordIDSegment stands for a path segment that looks like a record ID.
const recordIDSegment = ":id"

// isRecordID reports whether a path segment looks like a record ID: digits
// only, such as 4812, or letters, digits, hyphens, and underscores with at
// least two digits, such as C-100231 or a UUID. A word such as edit, a
// version such as v2 or v10, and a word with one digit such as step-1 or
// page2 are not; step-10 has the shape of C-100231 and counts as one.
func isRecordID(segment string) bool {
	digits, others := 0, 0
	for _, r := range segment {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '-', r == '_':
			others++
		default:
			return false
		}
	}
	switch {
	case digits == 0:
		return false
	case others == 0:
		return true
	case others == 1 && (segment[0] == 'v' || segment[0] == 'V'):
		return false
	}
	return digits >= 2
}
