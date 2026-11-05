// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import type { RightPanelId } from '@/contexts/RightPanelContext';
import {
  useWorkflowPanelMemory,
  WORKFLOW_PANEL_OPEN_KEY,
} from '../useYamlPanelMemory';

type Props = {
  fileName: string;
  activePanel: RightPanelId | null;
  restorable: boolean;
};

function renderMemory(initialProps: Props) {
  return renderHook((props: Props) => useWorkflowPanelMemory(props), {
    initialProps,
  });
}

const START: Props = {
  fileName: 'etl.yaml',
  activePanel: null,
  restorable: true,
};

describe('useWorkflowPanelMemory', () => {
  afterEach(() => localStorage.clear());

  // The editor opens the panel in its first render, and the Run panel yields
  // to it until it has taken the right of the zone.
  it('opens the panel that was open last, from the first render', () => {
    localStorage.setItem(WORKFLOW_PANEL_OPEN_KEY, 'true');
    const { result, rerender } = renderMemory(START);
    expect(result.current).toEqual({ open: true, pending: true });

    rerender({ ...START, activePanel: 'workflow' });
    expect(result.current).toEqual({ open: true, pending: false });
  });

  it('keeps it shut when a link opens another view', () => {
    localStorage.setItem(WORKFLOW_PANEL_OPEN_KEY, 'true');
    const { result } = renderMemory({ ...START, restorable: false });
    expect(result.current).toEqual({ open: false, pending: false });
  });

  it('stores each open and close of the panel', () => {
    const { rerender } = renderMemory(START);
    rerender({ ...START, activePanel: 'workflow' });
    expect(localStorage.getItem(WORKFLOW_PANEL_OPEN_KEY)).toBe('true');

    // The YAML panel takes its place.
    rerender({ ...START, activePanel: 'ndv' });
    expect(localStorage.getItem(WORKFLOW_PANEL_OPEN_KEY)).toBe('false');
  });

  it('stores nothing for the panels of another workflow', () => {
    localStorage.setItem(WORKFLOW_PANEL_OPEN_KEY, 'true');
    const { rerender } = renderMemory({ ...START, activePanel: 'workflow' });
    rerender({ ...START, fileName: 'other.yaml', activePanel: null });
    expect(localStorage.getItem(WORKFLOW_PANEL_OPEN_KEY)).toBe('true');
  });
});
