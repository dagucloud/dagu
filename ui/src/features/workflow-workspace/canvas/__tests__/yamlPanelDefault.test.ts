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

// The YAML panel opens as the user left it, and stays shut before any choice.
describe('yamlPanelWasOpen', () => {
  afterEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it.each([true, false])('is closed before any choice (laptop: %s)', (on) => {
    laptop(on);
    expect(yamlPanelWasOpen()).toBe(false);
  });

  it('follows the choice that the user left', () => {
    laptop(true);
    localStorage.setItem(YAML_PANEL_OPEN_KEY, 'true');
    expect(yamlPanelWasOpen()).toBe(true);
    localStorage.setItem(YAML_PANEL_OPEN_KEY, 'false');
    expect(yamlPanelWasOpen()).toBe(false);
  });
});
