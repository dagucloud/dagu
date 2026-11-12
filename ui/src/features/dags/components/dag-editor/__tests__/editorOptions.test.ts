// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { buildEditorOptions } from '../editorOptions';

describe('buildEditorOptions', () => {
  // The sticky line draws a half line over the first line of a step view.
  it('turns the sticky scroll off', () => {
    const options = buildEditorOptions({ readOnly: false, lineNumbers: true });
    expect(options.stickyScroll).toEqual({ enabled: false });
  });

  // The suggestion list is cut by the column of the node view otherwise; the
  // dimmed last line number fails the colour contrast of axe.
  it('draws the widgets in the body and keeps the last line number plain', () => {
    const options = buildEditorOptions({ readOnly: false, lineNumbers: true });
    expect(options.fixedOverflowWidgets).toBe(true);
    expect(options.renderFinalNewline).toBe('on');
  });

  it('follows the read-only and line number settings', () => {
    const options = buildEditorOptions({ readOnly: true, lineNumbers: false });
    expect(options).toMatchObject({
      readOnly: true,
      lineNumbers: 'off',
      quickSuggestions: false,
      renderValidationDecorations: 'off',
    });
  });
});
