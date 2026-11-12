// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec096_skills holds black-box conformance tests for Spec 096,
// "Skills". The tests run the Dagu binary of DAGU_BIN. They check that
// `dagu validate` and a step refuse a skill that is not in the Agent Skills
// format, that chat.completion serves a skill with load_skill and
// read_skill_file, that agent.run mounts the folder of a skill verbatim, and
// that the skill routes of `dagu server` give the hash, the conflict and the ZIP.
// A fake OpenAI-compatible endpoint and a fake claude CLI stand in for the
// providers. No test needs an external service.
package spec096_skills_test
