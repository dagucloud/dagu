// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, cleanup, renderHook } from '@testing-library/react';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import {
  WikiPageTabProvider,
  useWikiPageTabContext,
} from '../WikiPageTabContext';

function wrapperFor(storageKey: string) {
  return ({ children }: { children: React.ReactNode }) => (
    <WikiPageTabProvider storageKey={storageKey}>
      {children}
    </WikiPageTabProvider>
  );
}

describe('WikiPageTabProvider', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  afterEach(() => {
    cleanup();
    localStorage.clear();
  });

  it('ignores drafts saved after their tab closes', () => {
    const storageKey = 'dagu_wiki_tabs:test';
    const { result } = renderHook(() => useWikiPageTabContext(), {
      wrapper: wrapperFor(storageKey),
    });

    act(() => {
      result.current.openWikiPage('runbook.md', 'Runbook');
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
        tabs: [{ id: 'legacy', wikiPagePath: 'secret', title: 'Secret' }],
        activeTabId: 'legacy',
        drafts: [['legacy', 'legacy draft']],
        unsavedTabIds: ['legacy'],
      })
    );

    const { result } = renderHook(() => useWikiPageTabContext(), {
      wrapper: wrapperFor('dagu_wiki_tabs:user-a'),
    });

    expect(result.current.tabs).toHaveLength(0);
    expect(result.current.drafts).toHaveLength(0);
  });

  it('copies scoped legacy tabs into canonical storage without deleting them', () => {
    const legacyKey = 'dagu_doc_tabs:user-a';
    const storageKey = 'dagu_wiki_tabs:user-a';
    const legacyState = JSON.stringify({
      tabs: [{ id: 'legacy', docPath: 'runbooks/deploy', title: 'Deploy' }],
      activeTabId: 'legacy',
      drafts: [['legacy', 'legacy draft']],
      unsavedTabIds: ['legacy'],
    });
    localStorage.setItem(legacyKey, legacyState);

    const { result } = renderHook(() => useWikiPageTabContext(), {
      wrapper: wrapperFor(storageKey),
    });

    expect(result.current.tabs).toEqual([
      expect.objectContaining({
        id: 'legacy',
        wikiPagePath: 'runbooks/deploy',
        title: 'Deploy',
      }),
    ]);
    expect(result.current.activeTabId).toBe('legacy');
    expect(localStorage.getItem(storageKey)).not.toBeNull();
    expect(localStorage.getItem(legacyKey)).toBe(legacyState);
  });

  it('does not restore another user storage scope', () => {
    const userAKey = 'dagu_wiki_tabs:{"userId":"user-a"}';
    const userBKey = 'dagu_wiki_tabs:{"userId":"user-b"}';
    const userA = renderHook(() => useWikiPageTabContext(), {
      wrapper: wrapperFor(userAKey),
    });

    act(() => {
      userA.result.current.openWikiPage('runbook', 'Runbook');
    });
    const tabId = userA.result.current.tabs[0]!.id;
    act(() => {
      userA.result.current.setDraft(tabId, 'user A draft');
      userA.result.current.markTabUnsaved(tabId);
    });
    userA.unmount();

    const userB = renderHook(() => useWikiPageTabContext(), {
      wrapper: wrapperFor(userBKey),
    });
    expect(userB.result.current.tabs).toHaveLength(0);
    expect(userB.result.current.drafts).toHaveLength(0);
    userB.unmount();

    const restoredUserA = renderHook(() => useWikiPageTabContext(), {
      wrapper: wrapperFor(userAKey),
    });
    expect(restoredUserA.result.current.tabs).toHaveLength(1);
    expect(restoredUserA.result.current.getDraft(tabId)).toBe('user A draft');
  });

  it('reads and clears a direct-key draft through its scoped key', () => {
    const storageKey = 'dagu_wiki_tabs:user-a';
    const { result } = renderHook(() => useWikiPageTabContext(), {
      wrapper: wrapperFor(storageKey),
    });

    act(() => {
      result.current.openWikiPage('runbook', 'Runbook');
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
