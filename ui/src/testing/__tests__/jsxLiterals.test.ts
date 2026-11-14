// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { findJsxLiterals, type JsxLiteralHit } from '../jsxLiterals';

const fixture: Record<string, string> = {
  'text.tsx': `export function A() {
  return (
    <div>
      Hello world
      <span>   </span>
      <span>42</span>
      <span>— · : ( ) 3.5%</span>
      <b>{label}</b>
      <i>Été</i>
      <span>=&gt; &nbsp; &#8594; &#x2192;</span>
      <span>Fish &amp; chips</span>
    </div>
  );
}
`,
  'attributes.tsx': `export function B({ on }: { on: boolean }) {
  return (
    <form>
      <button aria-label="Close panel" title={'Close'} />
      <input placeholder={\`Search\`} />
      <img alt="Logo" src="/logo.svg" />
      <button aria-label={ts('Translated')} title={ts('Also translated')} />
      <button title={on ? 'Stop' : ts('Start')} />
      <button title={name ?? 'Fallback'} />
      <button aria-label={\`\${step} run\`} />
      <button aria-label="..." title="12" />
      <button aria-label={'Open ' + name} />
      <div data-testid="not-read" className="flex gap-2" role="group" />
    </form>
  );
}
`,
  'children.tsx': `export function E({ on, name }: { on: boolean; name: string }) {
  return (
    <p className={on ? 'bold' : 'thin'}>
      {on ? 'Running' : ts('Stopped')}
      {name ?? 'Unnamed'}
      {on && 'Live'}
      {'Run ' + name}
      {\`\${name} done\`}
      {on ? '—' : null}
      {/* a comment */}
    </p>
  );
}
`,
  'ignored.tsx': `export function C() {
  return (
    <div>
      <span title="Kept out" /> // i18n-ignore
      {/* i18n-ignore */}
      <span>Brand name</span>
      <span aria-label="Reported" />
    </div>
  );
}
`,
  'clean.tsx': `export function D() {
  const { ts } = useI18n();
  return <p title={ts('Saved')}>{ts('Saved')} {count}</p>;
}
`,
};

const summary = (hits: JsxLiteralHit[]) =>
  hits.map(({ kind, text, line }) => `${line} ${kind}: ${text}`);

describe('findJsxLiterals', () => {
  let srcDir: string;

  beforeAll(async () => {
    srcDir = await fs.promises.mkdtemp(
      path.join(os.tmpdir(), 'dagu-jsx-literals-')
    );
    for (const [file, content] of Object.entries(fixture)) {
      fs.writeFileSync(path.join(srcDir, file), content);
    }
  });

  afterAll(() => {
    fs.rmSync(srcDir, { recursive: true, force: true });
  });

  it('reports JSX text with letters and skips space, numbers, punctuation and character references', () => {
    const hits = findJsxLiterals(['text.tsx'], { srcDir });
    expect(summary(hits)).toEqual([
      '4 text: Hello world',
      '9 text: Été',
      '11 text: Fish &amp; chips',
    ]);
    expect(hits[0]).toMatchObject({ file: 'text.tsx', column: 7 });
  });

  it('reports literals in aria-label, title, placeholder and alt only', () => {
    expect(summary(findJsxLiterals(['attributes.tsx'], { srcDir }))).toEqual([
      '4 aria-label: Close panel',
      '4 title: Close',
      '5 placeholder: Search',
      '6 alt: Logo',
      '8 title: Stop',
      '9 title: Fallback',
      '10 aria-label: run',
      '12 aria-label: Open',
    ]);
  });

  it('reports literals in JSX child expressions and not in other attributes', () => {
    expect(summary(findJsxLiterals(['children.tsx'], { srcDir }))).toEqual([
      '4 text: Running',
      '5 text: Unnamed',
      '6 text: Live',
      '7 text: Run',
      '8 text: done',
    ]);
  });

  it("skips a line marked '// i18n-ignore' and the line under a marker line", () => {
    expect(summary(findJsxLiterals(['ignored.tsx'], { srcDir }))).toEqual([
      '7 aria-label: Reported',
    ]);
  });

  it('returns [] for translated JSX and takes absolute paths', () => {
    expect(findJsxLiterals([path.join(srcDir, 'clean.tsx')])).toEqual([]);
  });
});

describe('T30 files', () => {
  // The non-test .tsx files that T30 owns.
  it('have no untranslated JSX literal', () => {
    const here = path.dirname(fileURLToPath(import.meta.url));
    const src = path.resolve(here, '..', '..');
    const settings = 'features/workflow-workspace/settings';
    const files = [
      ...fs
        .readdirSync(path.join(src, settings))
        .filter((name) => name.endsWith('.tsx'))
        .map((name) => `${settings}/${name}`),
      'features/workflow-workspace/step/StepRunView.tsx',
      'features/workflow-workspace/step/StepLogPane.tsx',
      'features/visual-editor/canvas/ContextMenu.tsx',
    ];
    expect(files.length).toBeGreaterThan(3);
    expect(findJsxLiterals(files)).toEqual([]);
  });
});
