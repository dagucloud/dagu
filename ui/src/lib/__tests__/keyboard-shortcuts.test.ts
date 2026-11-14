// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { afterEach, describe, expect, it } from 'vitest';
import { shouldIgnoreKeyboardShortcuts } from '../keyboard-shortcuts';

function keyDown(init: KeyboardEventInit = {}): KeyboardEvent {
  return new KeyboardEvent('keydown', {
    key: 'Escape',
    cancelable: true,
    ...init,
  });
}

function focusIn(parent: HTMLElement, element: HTMLElement): void {
  parent.append(element);
  document.body.append(parent);
  element.focus();
}

afterEach(() => {
  document.body.replaceChildren();
});

describe('shouldIgnoreKeyboardShortcuts', () => {
  it('handles keys while focus is on the page', () => {
    focusIn(document.createElement('div'), document.createElement('button'));

    expect(shouldIgnoreKeyboardShortcuts(keyDown())).toBe(false);
  });

  it('ignores keys another handler already consumed', () => {
    const event = keyDown();
    event.preventDefault();

    expect(shouldIgnoreKeyboardShortcuts(event)).toBe(true);
  });

  it('ignores keys while a dialog owns focus', () => {
    for (const role of ['dialog', 'alertdialog']) {
      const dialog = document.createElement('div');
      dialog.setAttribute('role', role);
      focusIn(dialog, document.createElement('button'));

      expect(shouldIgnoreKeyboardShortcuts(keyDown()), role).toBe(true);
    }
  });

  it('ignores keys while the user types in a field', () => {
    focusIn(document.createElement('div'), document.createElement('input'));

    expect(shouldIgnoreKeyboardShortcuts(keyDown())).toBe(true);
  });
});
