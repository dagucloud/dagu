// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// The peek mounts on list pages: every text goes through the message
// catalogs, and the canvas loads lazily, so no static import reaches Mermaid.
// Mark a literal that is not a message with `// i18n-ignore` next to it.

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import {
  findForbiddenStaticImports,
  MERMAID_FORBIDDEN,
} from '@/testing/importGraph';
import { findJsxLiterals } from '@/testing/jsxLiterals';

const srcDir = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  '../../..'
);

const PEEK_DIR = 'features/peek';

/** The non-test sources of the peek folder. */
function peekSources(): string[] {
  return fs
    .readdirSync(path.join(srcDir, PEEK_DIR), { withFileTypes: true })
    .filter((entry) => entry.isFile() && /\.tsx?$/.test(entry.name))
    .map((entry) => `${PEEK_DIR}/${entry.name}`)
    .sort();
}

describe('peek boundary', () => {
  it('leaves no untranslated literal in its JSX', () => {
    const files = peekSources().filter((file) => file.endsWith('.tsx'));
    expect(files).toContain(`${PEEK_DIR}/PeekLink.tsx`);
    const hits = findJsxLiterals(files, { srcDir }).map(
      (hit) => `${hit.file}:${hit.line}:${hit.column} ${hit.kind} "${hit.text}"`
    );
    expect(hits).toEqual([]);
  });

  // The scan parses the whole reachable tree; under a parallel run it can
  // take longer than the default 5 s.
  it('reaches no Mermaid module through a static import', () => {
    expect(
      findForbiddenStaticImports(peekSources(), MERMAID_FORBIDDEN, { srcDir })
    ).toEqual([]);
  }, 30_000);
});
