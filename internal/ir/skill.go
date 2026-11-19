// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

// SkillEntry is one skill of a chat step or of an agent step, after the loader
// has read and checked its folder (Spec 096). The entry is a snapshot of one
// version of the skill: the run keeps it, so an edit of the folder, or a
// failover to another instance, does not change what the run received.
type SkillEntry struct {
	// Name is the name of the skill folder.
	Name string `json:"name"`
	// Description is the description of the front matter, on one line.
	Description string `json:"description"`
	// Body is the Markdown of SKILL.md after the front matter.
	Body string `json:"body"`
	// Hash identifies the version of the skill. It is the hash of Spec 096,
	// "The hash".
	Hash string `json:"hash,omitempty"`
	// Dir is the absolute path of the skill folder on the host that read it.
	// On a worker, it is the folder in the workspace bundle.
	Dir string `json:"dir,omitempty"`
	// Files lists every file of the skill, sorted by path. The text files hold
	// their content.
	Files []SkillFile `json:"files,omitempty"`
}

// SkillFile is one file of a skill.
type SkillFile struct {
	// Path is relative to the skill folder, with / separators.
	Path string `json:"path"`
	Size int64  `json:"size"`
	// SHA256 is the SHA-256 of the content, in lower case hexadecimal.
	SHA256 string `json:"sha256"`
	// Text is the content of a text file: SKILL.md, a file of references/ or a
	// file of scripts/. A binary file has no content here; Dir gives it.
	Text *string `json:"text,omitempty"`
}

// SkillRef names one version of a skill that a DAG reads. The loader records
// it so that a dispatch can check that the folder did not change between the
// read and the bundle.
type SkillRef struct {
	Name string
	Dir  string
	Hash string
}

// SkillDefinition is a skill that the effective catalog of a step offered. It
// is the record of the skill for the run.
type SkillDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Hash is the hash of the version of the skill that the step received.
	Hash string `json:"hash,omitempty"`
	// Omitted is set in the record of an older run, where a condition left the
	// skill out. The effective catalog no longer leaves a skill out.
	Omitted string `json:"omitted,omitempty"`
}
