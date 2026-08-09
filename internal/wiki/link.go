// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package wiki

import (
	"regexp"
	"strings"
)

// WikiLink is a single [[target]] reference found in page content.
// Target is returned raw and may carry a colon-delimited scheme (for
// example "dag:name") instead of a page ID.
type WikiLink struct {
	Target string
	Anchor string
	Label  string
}

// wikiLinkRegexp matches [[target]], [[target#anchor]], [[target|label]],
// and [[target#anchor|label]], with an optional leading ! marking an embed.
var wikiLinkRegexp = regexp.MustCompile(`(!?)\[\[([^\[\]|#]+)(#[^\[\]|]*)?(\|[^\[\]]*)?\]\]`)

// fenceRegexp matches a code fence opening or closing line.
var fenceRegexp = regexp.MustCompile("^\\s*(```|~~~)")

// ExtractWikiLinks returns the wiki links in content, in page order.
// Links inside fenced code blocks and inline code spans are ignored, as are
// ![[name]] embeds, which reference attachments rather than pages.
// Targets are returned raw, including scheme-prefixed targets.
func ExtractWikiLinks(content string) []WikiLink {
	var links []WikiLink
	inFence := false
	inlineCodeDelimiter := 0
	lines := strings.Split(content, "\n")
	for lineIndex, line := range lines {
		if fenceRegexp.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		line = stripInlineCode(line, lines[lineIndex+1:], &inlineCodeDelimiter)
		for _, m := range wikiLinkRegexp.FindAllStringSubmatch(line, -1) {
			if m[1] == "!" {
				continue
			}
			target := strings.TrimSpace(m[2])
			if target == "" {
				continue
			}
			links = append(links, WikiLink{
				Target: target,
				Anchor: strings.TrimSpace(strings.TrimPrefix(m[3], "#")),
				Label:  strings.TrimSpace(strings.TrimPrefix(m[4], "|")),
			})
		}
	}
	return links
}

func stripInlineCode(line string, followingLines []string, delimiter *int) string {
	var visible strings.Builder
	for i := 0; i < len(line); {
		if line[i] != '`' {
			if *delimiter == 0 {
				visible.WriteByte(line[i])
			}
			i++
			continue
		}
		start := i
		for i < len(line) && line[i] == '`' {
			i++
		}
		run := i - start
		if *delimiter == 0 {
			if hasInlineCodeCloser(line[i:], followingLines, run) {
				*delimiter = run
			} else {
				visible.WriteString(line[start:i])
			}
		} else if run == *delimiter {
			*delimiter = 0
		}
	}
	return visible.String()
}

func hasInlineCodeCloser(line string, followingLines []string, delimiter int) bool {
	if hasBacktickRun(line, delimiter) {
		return true
	}
	for _, line := range followingLines {
		if fenceRegexp.MatchString(line) {
			return false
		}
		if hasBacktickRun(line, delimiter) {
			return true
		}
	}
	return false
}

func hasBacktickRun(line string, delimiter int) bool {
	for i := 0; i < len(line); {
		if line[i] != '`' {
			i++
			continue
		}
		start := i
		for i < len(line) && line[i] == '`' {
			i++
		}
		if i-start == delimiter {
			return true
		}
	}
	return false
}
