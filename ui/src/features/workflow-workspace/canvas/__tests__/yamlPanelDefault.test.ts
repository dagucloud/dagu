// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { afterEach, describe, expect, it, vi } from 'vitest';
import { YAML_PANEL_OPEN_KEY, yamlPanelWasOpen } from '../useYamlPanelMemory';

function laptop(on: boolean) {
  window.matchMedia ??= (() => ({}) as MediaQueryList) as typeof matchMedia;
  vi.spyOn(window, 'matchMedia').mockImplementation(
    (query: string) => ({ matches: on, media: query }) as MediaQueryList
  );
}

// The owner decided that no panel opens by default, on a laptop either: the
// panel opens only as the user left it.
describe('yamlPanelWasOpen', () => {
  afterEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it('is closed by default, on a laptop too', () => {
    laptop(true);
    expect(yamlPanelWasOpen()).toBe(false);
    laptop(false);
    expect(yamlPanelWasOpen()).toBe(false);
  });

  it('keeps the choice of the user', () => {
    laptop(true);
    localStorage.setItem(YAML_PANEL_OPEN_KEY, 'true');
    expect(yamlPanelWasOpen()).toBe(true);
    localStorage.setItem(YAML_PANEL_OPEN_KEY, 'false');
    expect(yamlPanelWasOpen()).toBe(false);
  });
});
