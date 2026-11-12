// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { getYamlPathAtOffset } from '../../../../../hooks/useYamlCursorPath';
import { docsPathInScope } from '../docsPath';

const yaml = [
  'name: demo',
  'steps:',
  '  - name: a',
  '    run: echo a',
  '  - name: b',
  '    run: echo b',
  '',
].join('\n');

describe('docsPathInScope', () => {
  it('keeps the cursor path without a scope', () => {
    const info = getYamlPathAtOffset(yaml, 0);
    expect(docsPathInScope(info, null)).toBe(info);
  });

  // The cursor stays on a hidden line at the top: the docs show the step.
  it('follows the step when the cursor is outside it', () => {
    const info = getYamlPathAtOffset(yaml, 0);
    const docs = docsPathInScope(info, ['steps', '1']);
    expect(docs.path).toEqual(['steps', '1']);
    expect(docs.segments).toEqual([
      { key: 'steps', isArrayIndex: false },
      { key: '1', isArrayIndex: true },
    ]);
  });

  it('keeps a cursor path inside the step', () => {
    const info = getYamlPathAtOffset(yaml, yaml.indexOf('echo b'));
    expect(info.path.slice(0, 2)).toEqual(['steps', '1']);
    expect(docsPathInScope(info, ['steps', '1'])).toBe(info);
  });

  it('moves to the step when the cursor is in another step', () => {
    const info = getYamlPathAtOffset(yaml, yaml.indexOf('echo a'));
    expect(docsPathInScope(info, ['steps', '1']).path).toEqual(['steps', '1']);
  });

  it('does not take a map key for an array index', () => {
    const docs = docsPathInScope({ path: [], segments: [] }, [
      'steps',
      'build',
    ]);
    expect(docs.segments[1]).toEqual({ key: 'build', isArrayIndex: false });
  });
});
