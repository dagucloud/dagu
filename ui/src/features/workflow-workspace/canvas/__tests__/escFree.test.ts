// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { afterEach, describe, expect, it } from 'vitest';
import { escFree } from '../escFree';

function press(key: string, target?: EventTarget): KeyboardEvent {
  const event = new KeyboardEvent('keydown', {
    key,
    bubbles: true,
    cancelable: true,
  });
  if (target) Object.defineProperty(event, 'target', { value: target });
  return event;
}

afterEach(() => {
  document.body.innerHTML = '';
});

describe('escFree', () => {
  it('accepts Escape on the page', () => {
    expect(escFree(press('Escape', document.body))).toBe(true);
  });

  it('refuses another key and a key that a handler took', () => {
    expect(escFree(press('Enter', document.body))).toBe(false);
    const taken = press('Escape', document.body);
    taken.preventDefault();
    expect(escFree(taken)).toBe(false);
  });

  it('refuses a key in a text field', () => {
    const input = document.createElement('input');
    document.body.append(input);
    expect(escFree(press('Escape', input))).toBe(false);
  });

  it('refuses a key while a dialog or a menu is open', () => {
    const dialog = document.createElement('div');
    dialog.setAttribute('role', 'dialog');
    dialog.setAttribute('data-state', 'open');
    document.body.append(dialog);
    expect(escFree(press('Escape', document.body))).toBe(false);
    dialog.remove();

    const menu = document.createElement('div');
    menu.setAttribute('role', 'menu');
    document.body.append(menu);
    expect(escFree(press('Escape', document.body))).toBe(false);
  });
});
