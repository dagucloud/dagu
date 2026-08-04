// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, cleanup, renderHook } from '@testing-library/react';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { DocTabProvider, useDocTabContext } from '../DocTabContext';

function wrapperFor(storageKey: string) {
  return ({ children }: { children: React.ReactNode }) => (
    <DocTabProvider storageKey={storageKey}>{children}</DocTabProvider>
  );
}

describe('DocTabProvider', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  afterEach(() => {
    cleanup();
    localStorage.clear();
  });

  it('ignores drafts saved after their tab closes', () => {
    const storageKey = 'dagu_doc_tabs:test';
    const { result } = renderHook(() => useDocTabContext(), {
      wrapper: wrapperFor(storageKey),
    });

    act(() => {
      result.current.openDoc('runbook.md', 'Runbook');
    });

    const tabId = result.current.tabs[0]?.id;
    expect(tabId).toBeDefined();

    const draftKey = JSON.stringify({
      remoteNode: 'local',
      workspace: null,
      tabId,
    });

    act(() => {
      result.current.setDraft(draftKey, 'unsaved content');
    });
    expect(result.current.drafts.get(draftKey)).toBe('unsaved content');

    act(() => {
      result.current.closeTab(tabId!);
      result.current.setDraft(draftKey, 'discarded content');
    });

    expect(result.current.tabs).toHaveLength(0);
    expect(result.current.drafts).toHaveLength(0);
    expect(JSON.parse(localStorage.getItem(storageKey) ?? '{}').drafts).toEqual(
      []
    );
  });
});
