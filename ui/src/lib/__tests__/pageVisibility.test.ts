// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { afterEach, describe, expect, it } from 'vitest';
import { PAGE_HIDDEN_ATTRIBUTE, watchPageVisibility } from '../pageVisibility';

function setVisibility(state: 'visible' | 'hidden') {
  Object.defineProperty(document, 'visibilityState', {
    configurable: true,
    get: () => state,
  });
  document.dispatchEvent(new Event('visibilitychange'));
}

describe('watchPageVisibility', () => {
  afterEach(() => setVisibility('visible'));

  it('marks the root element while the tab is hidden', () => {
    const stop = watchPageVisibility();
    const root = document.documentElement;
    expect(root.hasAttribute(PAGE_HIDDEN_ATTRIBUTE)).toBe(false);
    setVisibility('hidden');
    expect(root.hasAttribute(PAGE_HIDDEN_ATTRIBUTE)).toBe(true);
    setVisibility('visible');
    expect(root.hasAttribute(PAGE_HIDDEN_ATTRIBUTE)).toBe(false);
    stop();
  });

  it('marks the root element at once when the tab starts hidden', () => {
    setVisibility('hidden');
    const stop = watchPageVisibility();
    expect(document.documentElement.hasAttribute(PAGE_HIDDEN_ATTRIBUTE)).toBe(
      true
    );
    stop();
    expect(document.documentElement.hasAttribute(PAGE_HIDDEN_ATTRIBUTE)).toBe(
      false
    );
  });

  it('stops the watch', () => {
    const stop = watchPageVisibility();
    stop();
    setVisibility('hidden');
    expect(document.documentElement.hasAttribute(PAGE_HIDDEN_ATTRIBUTE)).toBe(
      false
    );
  });
});
