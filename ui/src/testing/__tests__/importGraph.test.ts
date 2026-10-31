// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import {
  findForbiddenStaticImports,
  MERMAID_FORBIDDEN,
  type StaticImportHit,
} from '../importGraph';

const fixture: Record<string, string> = {
  'root.tsx': `
import type { T } from './typeOnly';
export type { U } from './exportTypeOnly';
import { type V } from './inlineTypeOnly';
import React from 'react';
import { a } from '@/lib/a';
import b from './dir';
export { c } from './reexport';
// import x from './commented';
/* import y from './commented'; */
const label = "import z from './commented'";
const Lazy = React.lazy(() => import('./lazy'));
const loaded = import('./dynamic');
export default function Root() { return <Lazy />; }
`,
  'root2.ts': `import { heavy } from './heavy';\n`,
  'lib/a.ts': `import '@scope/side/effect';\nimport mermaid from 'mermaid';\nexport const a = mermaid;\n`,
  'dir/index.tsx': `import { heavy } from '../heavy';\nexport default heavy;\n`,
  'heavy.tsx': `import { render } from 'mermaid/dist/render';\nexport const heavy = render;\n`,
  'reexport.ts': `export { c } from './deep/c';\n`,
  'deep/c.ts': `export const c = 1;\n`,
  'typeOnly.ts': `export type T = string;\n`,
  'exportTypeOnly.ts': `export type U = string;\n`,
  'inlineTypeOnly.ts': `export type V = string;\n`,
  'lazy.tsx': `export default function Lazy() { return null; }\n`,
  'dynamic.ts': `export const d = 1;\n`,
  'commented.ts': `export const x = 1;\n`,
};

const hitFor = (hits: StaticImportHit[], target: string) =>
  hits.filter((hit) => hit.chain[hit.chain.length - 1] === target);

describe('findForbiddenStaticImports', () => {
  let srcDir: string;

  beforeAll(async () => {
    srcDir = await fs.promises.mkdtemp(
      path.join(os.tmpdir(), 'dagu-import-graph-')
    );
    for (const [file, content] of Object.entries(fixture)) {
      const full = path.join(srcDir, file);
      fs.mkdirSync(path.dirname(full), { recursive: true });
      fs.writeFileSync(full, content);
    }
  });

  afterAll(() => {
    fs.rmSync(srcDir, { recursive: true, force: true });
  });

  it('ignores type-only imports, comments, strings, import() and React.lazy', () => {
    const ignored = [
      'typeOnly.ts',
      'exportTypeOnly.ts',
      'inlineTypeOnly.ts',
      'lazy.tsx',
      'dynamic.ts',
      'commented.ts',
    ];
    expect(
      findForbiddenStaticImports(['root.tsx'], ignored, { srcDir })
    ).toEqual([]);
  });

  it("resolves '@/', relative paths, index files and re-exports", () => {
    const hits = findForbiddenStaticImports(
      ['root.tsx'],
      ['lib/a.ts', 'dir/index.tsx', 'deep/c.ts', /^pkg:@scope\//],
      { srcDir }
    );
    expect(hits).toEqual([
      { root: 'root.tsx', chain: ['root.tsx', 'lib/a.ts'] },
      { root: 'root.tsx', chain: ['root.tsx', 'dir/index.tsx'] },
      {
        root: 'root.tsx',
        chain: ['root.tsx', 'lib/a.ts', 'pkg:@scope/side'],
      },
      { root: 'root.tsx', chain: ['root.tsx', 'reexport.ts', 'deep/c.ts'] },
    ]);
  });

  it('returns one shortest chain per forbidden module', () => {
    const hits = findForbiddenStaticImports(
      ['root.tsx', path.join(srcDir, 'root2.ts')],
      [/^pkg:mermaid$/, 'heavy.tsx'],
      { srcDir }
    );
    expect(hits).toHaveLength(2);
    expect(hitFor(hits, 'pkg:mermaid')).toEqual([
      { root: 'root.tsx', chain: ['root.tsx', 'lib/a.ts', 'pkg:mermaid'] },
    ]);
    expect(hitFor(hits, 'heavy.tsx')).toEqual([
      { root: 'root2.ts', chain: ['root2.ts', 'heavy.tsx'] },
    ]);
  });

  it('lists the Mermaid modules of the run page', () => {
    expect(MERMAID_FORBIDDEN).toEqual([
      /^pkg:mermaid$/,
      'components/ui/mermaid.tsx',
      'features/dags/components/visualization/Graph.tsx',
      'features/dags/components/visualization/DAGGraph.tsx',
      'features/dags/components/DAGStatus.tsx',
      'features/dags/components/dag-execution/DAGExecutionHistory.tsx',
    ]);
    // The real tree: the run view still reaches Mermaid, statically.
    const hits = findForbiddenStaticImports(
      ['features/dags/components/DAGStatus.tsx'],
      MERMAID_FORBIDDEN
    );
    expect(hitFor(hits, 'pkg:mermaid')).toHaveLength(1);
  }, 30_000);
});
