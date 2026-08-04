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
    expect(JSON.parse(localStorage.getItem(storageKey) ?? '{}').drafts).toEqual(
      [[draftKey, 'unsaved content']]
    );

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

  it('keeps scoped tab state isolated from legacy storage', () => {
    localStorage.setItem(
      'dagu_doc_tabs',
      JSON.stringify({
        tabs: [{ id: 'legacy', docPath: 'secret', title: 'Secret' }],
        activeTabId: 'legacy',
        drafts: [['legacy', 'legacy draft']],
        unsavedTabIds: ['legacy'],
      })
    );

    const { result } = renderHook(() => useDocTabContext(), {
      wrapper: wrapperFor('dagu_doc_tabs:user-a'),
    });

    expect(result.current.tabs).toHaveLength(0);
    expect(result.current.drafts).toHaveLength(0);
  });

  it('does not restore another user storage scope', () => {
    const userAKey = 'dagu_doc_tabs:{"userId":"user-a"}';
    const userBKey = 'dagu_doc_tabs:{"userId":"user-b"}';
    const userA = renderHook(() => useDocTabContext(), {
      wrapper: wrapperFor(userAKey),
    });

    act(() => {
      userA.result.current.openDoc('runbook', 'Runbook');
    });
    const tabId = userA.result.current.tabs[0]!.id;
    act(() => {
      userA.result.current.setDraft(tabId, 'user A draft');
      userA.result.current.markTabUnsaved(tabId);
    });
    userA.unmount();

    const userB = renderHook(() => useDocTabContext(), {
      wrapper: wrapperFor(userBKey),
    });
    expect(userB.result.current.tabs).toHaveLength(0);
    expect(userB.result.current.drafts).toHaveLength(0);
    userB.unmount();

    const restoredUserA = renderHook(() => useDocTabContext(), {
      wrapper: wrapperFor(userAKey),
    });
    expect(restoredUserA.result.current.tabs).toHaveLength(1);
    expect(restoredUserA.result.current.getDraft(tabId)).toBe('user A draft');
  });

  it('reads and clears a direct-key draft through its scoped key', () => {
    const storageKey = 'dagu_doc_tabs:user-a';
    const { result } = renderHook(() => useDocTabContext(), {
      wrapper: wrapperFor(storageKey),
    });

    act(() => {
      result.current.openDoc('runbook', 'Runbook');
    });
    const tabId = result.current.tabs[0]!.id;
    const scopedKey = JSON.stringify({
      tabId,
      remoteNode: 'local',
      workspace: 'default',
    });

    act(() => {
      result.current.setDraft(tabId, 'direct draft');
    });
    expect(result.current.getDraft(scopedKey)).toBe('direct draft');

    act(() => {
      result.current.clearDraft(scopedKey);
    });
    expect(result.current.getDraft(tabId)).toBeUndefined();
    expect(result.current.getDraft(scopedKey)).toBeUndefined();
  });
});
