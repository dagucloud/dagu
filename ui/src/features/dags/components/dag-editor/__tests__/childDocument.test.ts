// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { childDocumentSpan, replaceSpan } from '../childDocument';

const file = [
  'steps:',
  '  - call: child',
  '---',
  '# The child.',
  'name: child',
  'steps:',
  '  - command: echo child',
  '---',
  'name: last',
  'steps:',
  '  - command: echo last',
  '',
].join('\n');

describe('childDocumentSpan', () => {
  it('spans a child from the line after its marker to its end', () => {
    const span = childDocumentSpan(file, 'child')!;

    expect(file.slice(span.from, span.to)).toBe(
      '# The child.\nname: child\nsteps:\n  - command: echo child\n'
    );
  });

  it('spans the last child to the end of the file', () => {
    const span = childDocumentSpan(file, 'last')!;

    expect(file.slice(span.from, span.to)).toBe(
      'name: last\nsteps:\n  - command: echo last\n'
    );
  });

  it('finds no child of an unknown name, nor the parent', () => {
    expect(childDocumentSpan(file, 'missing')).toBeNull();
    expect(childDocumentSpan('name: main\nsteps: []\n', 'main')).toBeNull();
  });

  // An edit of the child goes back into the file, and the other documents
  // stay as they were.
  it('puts an edited child back into the file', () => {
    const span = childDocumentSpan(file, 'child')!;
    const next = replaceSpan(
      file,
      span,
      'name: child\nsteps:\n  - command: echo edited\n'
    );

    expect(next).toContain(
      '---\nname: child\nsteps:\n  - command: echo edited\n---\nname: last'
    );
    expect(next.startsWith('steps:\n  - call: child\n---\n')).toBe(true);
  });
});
