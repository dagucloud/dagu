// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  WORKFLOW_PANEL_OPEN_KEY,
  YAML_PANEL_OPEN_KEY,
  yamlPanelWasOpen,
} from '../useYamlPanelMemory';

function laptop(on: boolean) {
  window.matchMedia ??= (() => ({}) as MediaQueryList) as typeof matchMedia;
  vi.spyOn(window, 'matchMedia').mockImplementation(
    (query: string) => ({ matches: on, media: query }) as MediaQueryList
  );
}

// A laptop opens the YAML until the user closes it.
describe('yamlPanelWasOpen', () => {
  afterEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it('is open by default on a laptop', () => {
    laptop(true);
    expect(yamlPanelWasOpen()).toBe(true);
  });

  it('keeps a close of the user, or a Workflow panel that was the choice', () => {
    laptop(true);
    localStorage.setItem(YAML_PANEL_OPEN_KEY, 'false');
    expect(yamlPanelWasOpen()).toBe(false);
    localStorage.clear();
    localStorage.setItem(WORKFLOW_PANEL_OPEN_KEY, 'true');
    expect(yamlPanelWasOpen()).toBe(false);
  });

  it('is closed by default on a smaller window', () => {
    laptop(false);
    expect(yamlPanelWasOpen()).toBe(false);
  });
});
