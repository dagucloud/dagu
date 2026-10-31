// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// The token forms mirror the Go resolver in internal/cmn/value:
// - canonical `${steps.<id>.outputs.<name>}` (references.go, supportedStrictBinding;
//   specs/007-value-resolution-steps.md): exactly four segments, spaces inside
//   the braces are trimmed;
// - legacy `${<id>.<property>}` and `$<id>.<property>` (template.go,
//   referencePattern; resolve_step.go, resolveStepProperty), where the
//   property is stdout, stderr, exit_code, exitCode, output or outputs with an
//   optional `:start[:length]` slice, or a JSON path below output or outputs.
// Step ids are the lookup key at run time. A backslash run of odd length before
// `$` escapes the token (specs/003-value-resolution.md, "Escaped Dagu References").

const TOKEN = new RegExp(
  [
    // 1: spaces before the id, 2: id, 3: rest up to the closing brace
    String.raw`\$\{(\s*)steps\.([A-Za-z][A-Za-z0-9_]*)(\.outputs\.[A-Za-z_][A-Za-z0-9_]*\s*)\}`,
    // 4: id, 5: property
    String.raw`\$\{([A-Za-z0-9_]\w*)(\.[^}]+)\}`,
    // 6: id, 7: property
    String.raw`\$([A-Za-z0-9_]\w*)(\.[^\s]+)`,
  ].join('|'),
  'g'
);

const PLAIN_PROPERTY =
  /^\.(?:stdout|stderr|exit_code|exitCode|output|outputs)(?::\d+(?::\d*)?)?$/;
const PATH_PROPERTY = /^\.outputs?[.[]/;

function isStepProperty(rest: string): boolean {
  return PLAIN_PROPERTY.test(rest) || PATH_PROPERTY.test(rest);
}

function isEscaped(value: string, dollarIndex: number): boolean {
  let count = 0;
  for (let i = dollarIndex - 1; i >= 0 && value[i] === '\\'; i--) {
    count++;
  }
  return count % 2 === 1;
}

/** Calls `replace` for each token that refers to step `name`; returns the new text. */
function mapTokens(
  value: string,
  name: string,
  replace: (token: {
    kind: 'canonical' | 'legacy';
    braced: boolean;
    groups: string[];
  }) => string
): string {
  return value.replace(TOKEN, (match: string, ...args: unknown[]): string => {
    const offset = args[args.length - 2] as number;
    if (isEscaped(value, offset)) {
      return match;
    }
    const g = args.slice(0, 7) as Array<string | undefined>;
    if (g[1] !== undefined) {
      if (g[1] !== name) return match;
      return replace({
        kind: 'canonical',
        braced: true,
        groups: [g[0] ?? '', g[1], g[2] ?? ''],
      });
    }
    if (g[3] !== undefined) {
      if (g[3] !== name || !isStepProperty(g[4] ?? '')) return match;
      return replace({
        kind: 'legacy',
        braced: true,
        groups: ['', g[3], g[4] ?? ''],
      });
    }
    if (g[5] !== name || !isStepProperty(g[6] ?? '')) return match;
    return replace({
      kind: 'legacy',
      braced: false,
      groups: ['', g[5], g[6] ?? ''],
    });
  });
}

/**
 * Rewrites the tokens that refer to step `oldName` inside one string value so
 * that they refer to `newName`. Handles `${steps.<old>.outputs.<name>}`,
 * `${<old>.<property>}` and `$<old>.<property>`. Leaves everything else
 * unchanged: other steps, escaped tokens, params, env, consts and plain text.
 */
export function rewriteStepReferences(
  value: string,
  oldName: string,
  newName: string
): string {
  if (oldName === newName || !value.includes('$')) {
    return value;
  }
  return mapTokens(value, oldName, ({ kind, braced, groups }) => {
    if (kind === 'canonical') {
      return `\${${groups[0]}steps.${newName}${groups[2]}}`;
    }
    return braced ? `\${${newName}${groups[2]}}` : `$${newName}${groups[2]}`;
  });
}

/** The step ids that the `${steps.<id>.outputs.<name>}` tokens of `value` name. */
export function outputReferenceIds(value: string): string[] {
  if (!value.includes('$')) {
    return [];
  }
  const ids: string[] = [];
  for (const match of value.matchAll(TOKEN)) {
    const id = match[2];
    if (id !== undefined && !isEscaped(value, match.index)) {
      ids.push(id);
    }
  }
  return ids;
}

/**
 * Returns true when `value` has a token that `rewriteStepReferences` would
 * change for step `name`.
 */
export function mentionsStep(value: string, name: string): boolean {
  if (!value.includes('$')) {
    return false;
  }
  let found = false;
  mapTokens(value, name, ({ groups }) => {
    found = true;
    return groups.join('');
  });
  return found;
}
