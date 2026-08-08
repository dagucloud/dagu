// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package docs

import (
	"regexp"
	"strings"
)

// WikiLink is a single [[target]] reference found in document content.
// Target is returned raw and may carry a colon-delimited scheme (for
// example "dag:name") instead of a document ID.
type WikiLink struct {
	Target string
	Anchor string
	Label  string
}

// wikiLinkRegexp matches [[target]], [[target#anchor]], [[target|label]],
// and [[target#anchor|label]].
var wikiLinkRegexp = regexp.MustCompile(`\[\[([^\[\]|#]+)(#[^\[\]|]*)?(\|[^\[\]]*)?\]\]`)

// inlineCodeRegexp matches inline code spans so links inside them are ignored.
var inlineCodeRegexp = regexp.MustCompile("`[^`]*`")

// fenceRegexp matches a code fence opening or closing line.
var fenceRegexp = regexp.MustCompile("^\\s*(```|~~~)")

// ExtractWikiLinks returns the wiki links in content, in document order.
// Links inside fenced code blocks and inline code spans are ignored.
// Targets are returned raw, including scheme-prefixed targets.
func ExtractWikiLinks(content string) []WikiLink {
	var links []WikiLink
	inFence := false
	for line := range strings.SplitSeq(content, "\n") {
		if fenceRegexp.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		line = inlineCodeRegexp.ReplaceAllString(line, "")
		for _, m := range wikiLinkRegexp.FindAllStringSubmatch(line, -1) {
			target := strings.TrimSpace(m[1])
			if target == "" {
				continue
			}
			links = append(links, WikiLink{
				Target: target,
				Anchor: strings.TrimSpace(strings.TrimPrefix(m[2], "#")),
				Label:  strings.TrimSpace(strings.TrimPrefix(m[3], "|")),
			})
		}
	}
	return links
}
