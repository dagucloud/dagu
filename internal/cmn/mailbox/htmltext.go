// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mailbox

import (
	"io"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

var (
	blankLines = regexp.MustCompile(`\n{3,}`)
	spaceRuns  = regexp.MustCompile(`[ \t\r\f\v]+`)
)

// htmlToText renders an HTML email body as plain text: block elements start
// new lines, list items get bullets, and scripts and styles are dropped.
func htmlToText(r io.Reader) string {
	var b strings.Builder
	tokenizer := html.NewTokenizer(r)
	skip := 0
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			return finishText(b.String())
		case html.TextToken:
			if skip == 0 {
				b.WriteString(spaceRuns.ReplaceAllString(strings.ReplaceAll(string(tokenizer.Text()), "\n", " "), " "))
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := tokenizer.TagName()
			switch tag := string(name); tag {
			case "script", "style", "head", "title":
				skip++
			case "br":
				b.WriteString("\n")
			case "li":
				b.WriteString("\n- ")
			case "p", "div", "tr", "table", "ul", "ol", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote", "hr":
				b.WriteString("\n")
			case "td", "th":
				b.WriteString(" ")
			}
		case html.CommentToken, html.DoctypeToken:
		case html.EndTagToken:
			name, _ := tokenizer.TagName()
			switch tag := string(name); tag {
			case "script", "style", "head", "title":
				if skip > 0 {
					skip--
				}
			case "p", "div", "tr", "table", "ul", "ol", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote":
				b.WriteString("\n")
			}
		}
	}
}

func finishText(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	return strings.TrimSpace(blankLines.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}
