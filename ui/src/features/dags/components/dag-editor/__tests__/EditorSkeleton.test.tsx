// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { EditorSkeleton } from '../EditorSkeleton';

const TEXT = 'steps:\n  - id: build\n    run: make\n\n  - id: test';

// The rows of the skeleton: a row of a line has the bar of its number and,
// unless it is blank, the bar of its text; a lens row has one bar.
function rows() {
  return Array.from(
    screen.getByTestId('editor-skeleton').children
  ) as HTMLElement[];
}

describe('EditorSkeleton', () => {
  it('draws the text of each line at its indent and length', () => {
    render(<EditorSkeleton text={TEXT} />);
    const lines = rows();
    expect(lines).toHaveLength(5);
    const text = lines[1]!.lastElementChild as HTMLElement;
    expect(text.style.marginLeft).toBe('2ch');
    expect(text.style.width).toBe('11ch');
    // A blank line has the bar of its number only.
    expect(lines[3]!.children).toHaveLength(1);
  });

  it('puts the row of a lens above the line of each step', () => {
    render(<EditorSkeleton text={TEXT} lenses={[{ line: 2 }, { line: 5 }]} />);
    const lines = rows();
    expect(lines).toHaveLength(7);
    expect(lines[1]!.className).toContain('h-4');
    expect(lines[2]!.className).toContain('h-5');
    expect(lines[5]!.className).toContain('h-4');
  });
});
