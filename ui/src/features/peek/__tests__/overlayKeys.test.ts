// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { afterEach, describe, expect, it } from 'vitest';
import { overlayStepOfKey } from '../overlayKeys';

function keyOn(target: Element, key: string, init: KeyboardEventInit = {}) {
  const event = new KeyboardEvent('keydown', {
    key,
    bubbles: true,
    cancelable: true,
    ...init,
  });
  target.dispatchEvent(event);
  return event;
}

function step(target: Element, key: string, init?: KeyboardEventInit) {
  let found: -1 | 1 | null = null;
  const listen = (event: KeyboardEvent) => {
    found = overlayStepOfKey(event);
  };
  window.addEventListener('keydown', listen);
  keyOn(target, key, init);
  window.removeEventListener('keydown', listen);
  return found;
}

describe('overlayStepOfKey', () => {
  afterEach(() => {
    document.body.innerHTML = '';
  });

  it('steps up and down from the page', () => {
    expect(step(document.body, 'ArrowUp')).toBe(-1);
    expect(step(document.body, 'ArrowDown')).toBe(1);
    expect(step(document.body, 'ArrowDown', { shiftKey: true })).toBeNull();
  });

  it('leaves the arrows of a field and of tabs to them', () => {
    document.body.innerHTML =
      '<input id="field" /><div role="tablist"><button id="tab">a</button></div>';

    expect(step(document.getElementById('field')!, 'ArrowDown')).toBeNull();
    expect(step(document.getElementById('tab')!, 'ArrowDown')).toBeNull();
  });

  it('leaves an arrow that the page took', () => {
    document.body.innerHTML = '<div id="canvas" tabindex="0"></div>';
    const canvas = document.getElementById('canvas')!;
    canvas.addEventListener('keydown', (event) => event.preventDefault());

    expect(step(canvas, 'ArrowDown')).toBeNull();
  });

  it('leaves the keys to an open dialog', () => {
    document.body.innerHTML = '<div role="dialog" data-state="open"></div>';

    expect(step(document.body, 'ArrowDown')).toBeNull();
  });
});
