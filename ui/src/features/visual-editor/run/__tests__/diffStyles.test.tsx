// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render } from '@testing-library/react';
import ReactDiffViewer, { DiffMethod } from 'react-diff-viewer-continued';
import { describe, expect, it } from 'vitest';
import { diffViewerStyles } from '../diffStyles';

const before = 'a: 1\nb: 2\nc: 3\n';
// `b` changes, `d` is added, and a blank line is added.
const after = 'a: 1\nb: 9\nc: 3\n\nd: 4\n';

function renderDiff(splitView: boolean): HTMLElement {
  const { container } = render(
    <ReactDiffViewer
      oldValue={before}
      newValue={after}
      splitView={splitView}
      compareMethod={DiffMethod.LINES}
      showDiffOnly={false}
      styles={diffViewerStyles}
    />
  );
  return container;
}

function cellsOf(container: HTMLElement, label: string): HTMLElement[] {
  return [...container.querySelectorAll<HTMLElement>('td')].filter((td) =>
    td.className.includes(`-${label}`)
  );
}

/** The CSS that the viewer put in the page. */
function emittedCss(): string {
  return [...document.querySelectorAll('style')]
    .map((style) => style.textContent ?? '')
    .join('\n');
}

describe.each([
  ['split', true],
  ['unified', false],
])('diff colours in the %s view', (_name, split) => {
  it('marks added, removed and unchanged lines with classes', () => {
    const container = renderDiff(split);
    expect(cellsOf(container, 'diff-added').length).toBeGreaterThan(0);
    expect(cellsOf(container, 'diff-removed').length).toBeGreaterThan(0);
    // Each content cell of an unchanged line has neither class.
    const plain = [...container.querySelectorAll<HTMLElement>('td')].filter(
      (td) => td.textContent?.includes('a: 1')
    );
    expect(plain.length).toBeGreaterThan(0);
    for (const td of plain) {
      expect(td.className).not.toMatch(/-diff-(added|removed)/);
    }
  });

  it('colours the sign cell and keeps the colour of a blank added line', () => {
    const container = renderDiff(split);
    const markers = cellsOf(container, 'marker');
    expect(markers.some((td) => td.className.includes('-diff-added'))).toBe(
      true
    );
    expect(markers.some((td) => td.className.includes('-diff-removed'))).toBe(
      true
    );
    // The blank line is an added line that is also an empty line.
    expect(
      cellsOf(container, 'empty-line').some((td) =>
        td.className.includes('-diff-added')
      )
    ).toBe(true);
  });
});

describe('diff styles', () => {
  it('takes every colour from a theme token', () => {
    renderDiff(true);
    const css = emittedCss();
    expect(css).toContain('var(--success)');
    expect(css).toContain('var(--destructive)');
    // The 2 px bar on the left of a changed line.
    expect(css).toMatch(/inset 2px 0 0 var\(--success\)/);
    expect(css).toMatch(/inset 2px 0 0 var\(--destructive\)/);
    // The blank added line and the numbers keep their colour and width.
    expect(css).toContain('tabular-nums');
    expect(css).toMatch(
      /\[class\*="-diff-added"\]\{background:color-mix\(in srgb, var\(--success\) 16%/
    );
  });
});
