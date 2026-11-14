// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agentstep

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"slices"
)

// UnusedVariables returns the variables an instruction names, once each in
// the order named, that none of texts references: the variables a
// recording of the instruction did nothing with, such as an item it never
// chose because its list already showed the value.
func UnusedVariables(instruction string, texts []string) []string {
	used := map[string]bool{}
	for _, text := range texts {
		for _, name := range VariableReferences(text) {
			used[name] = true
		}
	}
	var unused []string
	for _, name := range VariableReferences(instruction) {
		if !used[name] && !slices.Contains(unused, name) {
			unused = append(unused, name)
		}
	}
	return unused
}

// Assumed is what a recording took for granted: the values variables it did
// nothing with had when it was made, so a replay is right only where they
// still hold. A value is kept as a digest under a salt of the recording's
// own, never in the clear, since it may be secret.
type Assumed struct {
	Salt    string            `json:"salt"`
	Digests map[string]string `json:"digests"`
}

// Assume returns what a recording made with values takes for granted about
// the variables names, or nil when names is empty.
func Assume(names []string, values map[string]string) *Assumed {
	if len(names) == 0 {
		return nil
	}
	a := &Assumed{Salt: rand.Text(), Digests: make(map[string]string, len(names))}
	for _, name := range names {
		a.Digests[name] = a.digest(name, values[name])
	}
	return a
}

// Unmet returns the first of names whose value in values is not the one the
// recording assumed, or one it assumed nothing about, and "" when every one
// holds.
func (a *Assumed) Unmet(names []string, values map[string]string) string {
	for _, name := range names {
		if a == nil || !hmac.Equal([]byte(a.Digests[name]), []byte(a.digest(name, values[name]))) {
			return name
		}
	}
	return ""
}

func (a *Assumed) digest(name, value string) string {
	mac := hmac.New(sha256.New, []byte(a.Salt))
	mac.Write([]byte(name + "\x00" + value))
	return hex.EncodeToString(mac.Sum(nil))
}
