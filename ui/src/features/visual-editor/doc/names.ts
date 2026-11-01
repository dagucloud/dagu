// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Rules come from specs/009-step-reference.md ("Step Identity") and from the
// Go validators in internal/spec/validator.go (validateStep, ValidateStepID,
// collectNamesAndIDs) and internal/ir/agent.go (IsSynthesizedAgentStep).

const MAX_NAME_BYTES = 255;
const MAX_ID_BYTES = 40;
const STEP_ID_PATTERN = /^[A-Za-z][A-Za-z0-9_]*$/;
const RESERVED_IDS = new Set([
  'env',
  'params',
  'args',
  'stdout',
  'stderr',
  'output',
  'outputs',
]);
// Names of the scaffolding steps of a `type: agent` DAG (ir.AgentStepName,
// ir.AskUserStepName). The Go validator rejects them in every other DAG.
const RESERVED_NAMES = new Set(['__agent__', 'ask_user']);

const encoder = new TextEncoder();

function utf8Length(value: string): number {
  return encoder.encode(value).length;
}

/**
 * Checks a step `name`. Returns null when the name is valid, else a short
 * English message. Go trims names before validation, so a name with leading
 * or trailing whitespace is rejected here instead of being trimmed.
 */
export function validateStepName(name: string): string | null {
  if (name.trim() === '') {
    return 'Step name must not be empty.';
  }
  if (name !== name.trim()) {
    return 'Step name must not start or end with whitespace.';
  }
  if (utf8Length(name) > MAX_NAME_BYTES) {
    return `Step name must be at most ${MAX_NAME_BYTES} bytes.`;
  }
  if (RESERVED_NAMES.has(name)) {
    return `"${name}" is a reserved step name.`;
  }
  return null;
}

/**
 * Checks a step `id`. Returns null when the id is valid, else a short
 * English message. Reserved words match without case.
 */
export function validateStepId(id: string): string | null {
  if (!STEP_ID_PATTERN.test(id)) {
    return "Step ID must start with a letter and use only letters, digits and '_'.";
  }
  if (utf8Length(id) > MAX_ID_BYTES) {
    return `Step ID must be at most ${MAX_ID_BYTES} bytes.`;
  }
  if (RESERVED_IDS.has(id.toLowerCase())) {
    return `"${id}" is a reserved word and cannot be a step ID.`;
  }
  return null;
}

// Leaves room for a `_<n>` suffix inside the 255-byte limit.
const MAX_BASE_BYTES = 200;

function sanitizeBase(base: string): string {
  const cleaned = base
    .replace(/[^\p{L}\p{M}\p{N}_]+/gu, '_')
    .replace(/^_+|_+$/g, '');
  let out = '';
  let bytes = 0;
  for (const ch of cleaned) {
    const size = utf8Length(ch);
    if (bytes + size > MAX_BASE_BYTES) break;
    out += ch;
    bytes += size;
  }
  return out === '' ? 'step' : out;
}

/**
 * Returns a valid step name that is not in `taken`: the sanitized `base`, else
 * `base_2`, `base_3` and so on. Sanitizing replaces each run of characters
 * other than letters, combining marks, digits and `_` with one `_`
 * (`http.request` gives `http_request`). The caller puts step names and ids
 * in `taken`, because a name must differ from every id.
 */
export function uniqueStepName(base: string, taken: Iterable<string>): string {
  const used = new Set(taken);
  const root = sanitizeBase(base);
  for (let n = 1; ; n++) {
    const candidate = n === 1 ? root : `${root}_${n}`;
    if (!used.has(candidate) && validateStepName(candidate) === null) {
      return candidate;
    }
  }
}

/**
 * The name of a command step that has no action: `cmd_<n>` with the first n
 * from `position` on that is free. The server names an unnamed command step
 * the same way, so one rule gives every command step its name.
 */
export function uniqueCommandName(
  position: number,
  taken: Iterable<string>
): string {
  const used = new Set(taken);
  for (let n = Math.max(position, 1); ; n++) {
    const candidate = `cmd_${n}`;
    if (!used.has(candidate)) return candidate;
  }
}
