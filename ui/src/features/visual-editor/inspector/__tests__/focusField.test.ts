// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { focusBox } from '../focusField';

function box(html: string): HTMLElement {
  const root = document.createElement('div');
  root.innerHTML = html;
  document.body.appendChild(root);
  return root.firstElementChild as HTMLElement;
}

describe('focusBox', () => {
  it('focuses the first control of the box', () => {
    const el = box(
      '<div><button id="a">a</button><button id="b">b</button></div>'
    );
    focusBox(el);
    expect(document.activeElement?.id).toBe('a');
  });

  it('prefers the control that marks itself, over an earlier button', () => {
    const el = box(
      '<div><button id="del">x</button><input id="add" data-reveal-focus /></div>'
    );
    focusBox(el);
    expect(document.activeElement?.id).toBe('add');
  });

  it('focuses the box itself when it asks for it, and does not press a button', () => {
    const el = box(
      '<div id="list" data-reveal-self><button id="del">x</button></div>'
    );
    focusBox(el);
    expect(document.activeElement?.id).toBe('list');
  });

  it('focuses the box when it has no control', () => {
    const el = box('<div id="empty"><p>none</p></div>');
    focusBox(el);
    expect(document.activeElement?.id).toBe('empty');
  });
});
