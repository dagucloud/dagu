// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package skill reads, checks, hashes and writes skill folders. A skill folder
// follows the Agent Skills format (https://agentskills.io/specification) with
// the restrictions of Spec 096. The folder on disk is the only source of truth.
package skill

import (
	"fmt"
	"sort"
	"strings"
)

const (
	// FileName is the name of the file that defines a skill.
	FileName = "SKILL.md"
	// DirReferences, DirAssets and DirScripts are the three directories that a
	// skill folder can hold besides SKILL.md.
	DirReferences = "references"
	DirAssets     = "assets"
	DirScripts    = "scripts"
	// KeepFileName is the only hidden file that a skill can hold.
	KeepFileName = ".gitkeep"
)

// Limits of the format. Spec 096, "The format" and "The folder".
const (
	MaxNameLength          = 64
	MaxDescriptionLength   = 1024
	MaxCompatibilityLength = 500
	MaxLicenseLength       = 128
	// MaxBodyBytes is the size limit of the Markdown body.
	MaxBodyBytes = 64 * 1024
	// WarnBodyLines and WarnBodyTokens are the recommended limits of the body.
	WarnBodyLines  = 500
	WarnBodyTokens = 5000
	// WarnDescriptionLength is the length under which the description is
	// probably too short to route a task.
	WarnDescriptionLength = 20

	// MaxFileBytes is the size limit of one file. MaxTotalBytes is the limit of
	// all files of one skill. MaxFiles is the limit of the number of files.
	MaxFileBytes  = 4 << 20
	MaxTotalBytes = 16 << 20
	MaxFiles      = 256
	// MaxPathLength is the limit of the length of a relative path, in bytes.
	MaxPathLength = 512
	// MaxReadFileBytes is the size limit of a file that the tool
	// read_skill_file returns.
	MaxReadFileBytes = 256 * 1024
	// MaxDownloadSkills and MaxDownloadBytes limit one ZIP download.
	MaxDownloadSkills = 100
	MaxDownloadBytes  = 256 << 20

	// MaxSkillsPerStep and CatalogTokenBudget limit the skills of one step.
	MaxSkillsPerStep   = 20
	CatalogTokenBudget = 2000
)

// Severity of an issue.
const (
	SeverityError   = "error"
	SeverityWarning = "warning"
)

// Rule identifiers. An issue carries one of them.
const (
	RuleFolderLink        = "folder-link"
	RuleSkillMissing      = "skill-md-missing"
	RuleNotUTF8           = "not-utf8"
	RuleFrontMatterStart  = "front-matter-start"
	RuleFrontMatterEnd    = "front-matter-end"
	RuleFrontMatterYAML   = "front-matter-yaml"
	RuleFrontMatterKey    = "front-matter-key"
	RuleFrontMatterDup    = "front-matter-duplicate-key"
	RuleNameMissing       = "name-missing"
	RuleNameType          = "name-type"
	RuleNameLength        = "name-length"
	RuleNameCharset       = "name-charset"
	RuleNameHyphenEdge    = "name-hyphen-edge"
	RuleNameHyphenDouble  = "name-hyphen-double"
	RuleNameFolder        = "name-folder"
	RuleDescriptionMiss   = "description-missing"
	RuleDescriptionType   = "description-type"
	RuleDescriptionLength = "description-length"
	RuleDescriptionXML    = "description-xml"
	RuleDescriptionShort  = "description-short"
	RuleLicense           = "license"
	RuleCompatibility     = "compatibility"
	RuleMetadata          = "metadata"
	RuleAllowedTools      = "allowed-tools"
	RuleBodyEmpty         = "body-empty"
	RuleBodyTooLarge      = "body-too-large"
	RuleBodyLines         = "body-lines"
	RuleBodyTokens        = "body-tokens"
	RuleRootEntry         = "root-entry"
	RuleSymlink           = "symlink"
	RuleHardlink          = "hardlink"
	RuleSpecialFile       = "special-file"
	RuleHiddenFile        = "hidden-file"
	RulePath              = "path"
	RuleFileTooLarge      = "file-too-large"
	RuleTooManyFiles      = "too-many-files"
	RuleTotalTooLarge     = "total-too-large"
	RuleReferenceDepth    = "reference-depth"
	RuleReadTooLarge      = "file-too-large-to-read"
	RuleChanging          = "changing"
	RuleUnreadable        = "unreadable"
)

// Issue is one finding of the check of a skill.
type Issue struct {
	// Severity is SeverityError or SeverityWarning.
	Severity string `json:"severity"`
	// Rule is the identifier of the rule that the skill breaks.
	Rule string `json:"rule"`
	// File is the path of the file, relative to the skill folder. It is empty
	// when the issue concerns the folder.
	File string `json:"file,omitempty"`
	// Message tells what is wrong and what the rule is.
	Message string `json:"message"`
}

// String formats the issue for a log or an error message.
func (i Issue) String() string {
	where := i.File
	if where == "" {
		where = "folder"
	}
	return fmt.Sprintf("%s: %s (rule %s)", where, i.Message, i.Rule)
}

// FrontMatter is the checked front matter of SKILL.md. The fields that the
// file does not set are empty.
type FrontMatter struct {
	Name          string
	Description   string
	License       string
	Compatibility string
	Metadata      map[string]string
	AllowedTools  string
}

// File is one regular file of a skill folder.
type File struct {
	// Path is relative to the skill folder, with / separators.
	Path string `json:"path"`
	Size int64  `json:"size"`
	// SHA256 is the SHA-256 of the content, in lower case hexadecimal.
	SHA256 string `json:"sha256"`
	// Text is true when the file is valid UTF-8 text that the model can read
	// (SKILL.md, references and scripts).
	Text bool `json:"text"`
}

// Skill is the result of the read of one skill folder. A skill with an error
// is refused: nothing loads it.
type Skill struct {
	// Name is the name of the folder.
	Name string
	// Dir is the absolute path of the folder.
	Dir string
	// Meta is the front matter. It is complete only when the front matter is
	// valid.
	Meta FrontMatter
	// Body is the Markdown after the front matter.
	Body string
	// Hash identifies the version of the skill. See Hash.
	Hash string
	// Files lists every regular file, sorted by path. SKILL.md is in the list.
	Files []File
	// Issues lists the errors and the warnings, errors first.
	Issues []Issue

	// text holds the content of the files that have Text set.
	text map[string][]byte
}

// Errors returns the issues of severity error.
func (s *Skill) Errors() []Issue { return s.filter(SeverityError) }

// Warnings returns the issues of severity warning.
func (s *Skill) Warnings() []Issue { return s.filter(SeverityWarning) }

func (s *Skill) filter(severity string) []Issue {
	var out []Issue
	for _, issue := range s.Issues {
		if issue.Severity == severity {
			out = append(out, issue)
		}
	}
	return out
}

// Valid reports whether the skill has no error.
func (s *Skill) Valid() bool { return len(s.Errors()) == 0 }

// Err returns nil for a valid skill. Otherwise it returns an error that names
// the skill, each file and each rule that the skill breaks.
func (s *Skill) Err() error {
	errs := s.Errors()
	if len(errs) == 0 {
		return nil
	}
	parts := make([]string, len(errs))
	for i, issue := range errs {
		parts[i] = issue.String()
	}
	return fmt.Errorf("skill %q is refused because it is not in the Agent Skills format: %s",
		s.Name, strings.Join(parts, "; "))
}

// Text returns the content of a text file of the skill. The read of the folder
// kept it, so it is the content that the hash covers.
func (s *Skill) Text(path string) ([]byte, bool) {
	data, ok := s.text[path]
	return data, ok
}

// Raw returns the exact bytes of SKILL.md.
func (s *Skill) Raw() []byte { return s.text[FileName] }

// File returns the entry of a file.
func (s *Skill) File(path string) (File, bool) {
	i := sort.Search(len(s.Files), func(i int) bool { return s.Files[i].Path >= path })
	if i < len(s.Files) && s.Files[i].Path == path {
		return s.Files[i], true
	}
	return File{}, false
}

// Stats returns the sizes of the body of SKILL.md.
func (s *Skill) Stats() Stats {
	return Stats{
		BodyBytes:  len(s.Body),
		BodyLines:  BodyLines(s.Body),
		BodyTokens: EstimateTokens(len(s.Body)),
	}
}

// Stats holds the sizes of a body.
type Stats struct {
	BodyBytes  int
	BodyLines  int
	BodyTokens int
}

// BodyLines counts the lines of a body.
func BodyLines(body string) int {
	if body == "" {
		return 0
	}
	n := strings.Count(body, "\n")
	if !strings.HasSuffix(body, "\n") {
		n++
	}
	return n
}

// EstimateTokens estimates the number of tokens of n bytes of text. The
// estimate is bytes divided by 3.7, rounded up. It is approximate.
func EstimateTokens(n int) int {
	if n <= 0 {
		return 0
	}
	return (n*10 + 36) / 37
}

// IsTextPath reports whether the model can read the file at the path: the
// path is SKILL.md or lies under references/ or scripts/.
func IsTextPath(rel string) bool {
	if rel == FileName {
		return true
	}
	top, _, ok := strings.Cut(rel, "/")
	return ok && (top == DirReferences || top == DirScripts)
}

// IsReadable reports whether read_skill_file serves the path: a file of
// references/ or scripts/.
func IsReadable(rel string) bool { return rel != FileName && IsTextPath(rel) }
