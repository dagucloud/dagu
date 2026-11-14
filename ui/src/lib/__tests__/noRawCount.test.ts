// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// A count of executions can reach millions (spec 083, Compact Count). No
// screen prints it raw: it goes through formatCompactCount, or CompactCount
// and RepeatBadge, which call it. This scan fails on the raw forms. The
// `×{...limit}` of a repeat policy is a limit, not a count, and is allowed.

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const srcDir = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  '../..'
);

const RAW_FORMS: RegExp[] = [
  // `×${count}` in a template literal.
  /×\$\{(?!formatCompactCount\()/,
  // `×{count}` in JSX, but not the limit of a repeat policy.
  /×\{(?!formatCompactCount\()(?![\w.?]*limit\b)/,
  // The count of a node, written as it is or with a fallback such as `?? 0`.
  // A prop (`count={...}`) is not text.
  /String\([\w.?]*doneCount(?:\s*(?:\?\?|\|\|)\s*\w+)?\)/,
  /(?<![=\w])\{[\w.?]*doneCount(?:\s*(?:\?\?|\|\|)\s*\w+)?\}/,
];

/** The raw forms that a source text contains. */
export function rawCountForms(source: string): string[] {
  return RAW_FORMS.filter((re) => re.test(source)).map((re) => String(re));
}

function sources(dir: string): string[] {
  const out: string[] = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      if (entry.name !== '__tests__' && entry.name !== 'node_modules') {
        out.push(...sources(full));
      }
    } else if (/\.(ts|tsx)$/.test(entry.name) && !/\.test\./.test(entry.name)) {
      out.push(full);
    }
  }
  return out;
}

describe('raw execution counts', () => {
  it('finds the raw forms in a probe', () => {
    expect(rawCountForms('`×${count}`')).toHaveLength(1);
    expect(rawCountForms('<b>×{node.doneCount}</b>')).toHaveLength(2);
    expect(rawCountForms('String(node.doneCount)')).toHaveLength(1);
    expect(rawCountForms('<b>{node.doneCount ?? 0}</b>')).toHaveLength(1);
    expect(rawCountForms('String(node.doneCount || 0)')).toHaveLength(1);
    expect(rawCountForms('<X count={node.doneCount} />')).toEqual([]);
    expect(rawCountForms('<X count={node.doneCount ?? 0} />')).toEqual([]);
    expect(rawCountForms('`×${formatCompactCount(count)}`')).toEqual([]);
    expect(rawCountForms('×{step.repeatPolicy.limit}')).toEqual([]);
  });

  it('leaves no screen that prints a count of executions raw', () => {
    const hits = sources(srcDir)
      .filter((file) => !file.endsWith('schema.ts'))
      .flatMap((file) =>
        rawCountForms(fs.readFileSync(file, 'utf8')).map(
          (form) => `${path.relative(srcDir, file)} ${form}`
        )
      );
    expect(hits).toEqual([]);
  });
});
