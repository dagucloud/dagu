// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React, {
  createContext,
  useContext,
  useState,
  useCallback,
  useEffect,
  useRef,
} from 'react';
import { useUnsavedChanges } from './UnsavedChangesContext';

export interface DocTab {
  id: string;
  docPath: string;
  title: string;
  workspace?: string | null;
}

interface DocTabContextType {
  tabs: DocTab[];
  activeTabId: string | null;
  openDoc: (docPath: string, title: string, workspace?: string | null) => void;
  closeTab: (tabId: string) => void;
  closeAllTabs: () => void;
  closeOtherTabs: (keepTabId: string) => void;
  setActiveTab: (tabId: string) => void;
  getActiveDocPath: () => string | null;
  updateTab: (
    tabId: string,
    updates: Partial<Pick<DocTab, 'docPath' | 'title' | 'workspace'>>
  ) => void;

  // Draft content persistence
  drafts: ReadonlyMap<string, string>;
  setDraft: (tabId: string, content: string) => void;
  clearDraft: (tabId: string) => void;
  getDraft: (tabId: string) => string | undefined;

  // Per-tab unsaved tracking
  unsavedTabIds: ReadonlySet<string>;
  markTabUnsaved: (tabId: string) => void;
  markTabSaved: (tabId: string) => void;
  isTabUnsaved: (tabId: string) => boolean;
}

const STORAGE_KEY = 'dagu_doc_tabs';

const DocTabContext = createContext<DocTabContextType | null>(null);

export function useDocTabContext() {
  const context = useContext(DocTabContext);
  if (!context) {
    throw new Error('useDocTabContext must be used within a DocTabProvider');
  }
  return context;
}

interface StoredTabState {
  tabs: DocTab[];
  activeTabId: string | null;
  drafts?: [string, string][];
  unsavedTabIds?: string[];
}

interface DocTabState {
  tabs: DocTab[];
  activeTabId: string | null;
  drafts: Map<string, string>;
  unsavedTabIds: Set<string>;
}

function generateTabId(): string {
  return `doc-${Date.now()}-${Math.random().toString(36).substring(2, 11)}`;
}

function readStoredTabState(storageKey: string): StoredTabState | null {
  try {
    const stored = localStorage.getItem(storageKey);
    if (stored) {
      return JSON.parse(stored) as StoredTabState;
    }
  } catch {
    // Ignore parse errors
  }
  return null;
}

function writeStoredTabState(storageKey: string, state: StoredTabState): void {
  try {
    localStorage.setItem(storageKey, JSON.stringify(state));
  } catch {
    // Ignore persistence errors (quota/private mode)
  }
}

function restoreDocTabState(storageKey: string): DocTabState {
  const stored = readStoredTabState(storageKey);
  const tabs = stored?.tabs ?? [];
  const activeTabId =
    stored?.activeTabId && tabs.some((tab) => tab.id === stored.activeTabId)
      ? stored.activeTabId
      : null;
  return {
    tabs,
    activeTabId,
    drafts: new Map(stored?.drafts ?? []),
    unsavedTabIds: new Set(stored?.unsavedTabIds ?? []),
  };
}

function storeDocTabState(storageKey: string, state: DocTabState): void {
  writeStoredTabState(storageKey, {
    tabs: state.tabs,
    activeTabId: state.activeTabId,
    drafts: Array.from(state.drafts.entries()),
    unsavedTabIds: Array.from(state.unsavedTabIds),
  });
}

function draftTabIdFromKey(key: string): string {
  try {
    const parsed = JSON.parse(key) as { tabId?: unknown };
    return typeof parsed.tabId === 'string' ? parsed.tabId : key;
  } catch {
    return key;
  }
}

function draftKeyMatchesTabId(key: string, tabId: string): boolean {
  return draftTabIdFromKey(key) === tabId;
}

export function DocTabProvider({
  children,
  storageKey = STORAGE_KEY,
}: {
  children: React.ReactNode;
  storageKey?: string;
}) {
  const { setHasUnsavedChanges } = useUnsavedChanges();
  const [state, setState] = useState<DocTabState>(() =>
    restoreDocTabState(storageKey)
  );
  const stateRef = useRef(state);
  stateRef.current = state;

  const commitState = useCallback(
    (nextState: DocTabState) => {
      stateRef.current = nextState;
      setState(nextState);
      storeDocTabState(storageKey, nextState);
    },
    [storageKey]
  );

  useEffect(() => {
    setHasUnsavedChanges(state.unsavedTabIds.size > 0);
    return () => {
      setHasUnsavedChanges(false);
    };
  }, [state.unsavedTabIds, setHasUnsavedChanges]);

  const openDoc = useCallback(
    (docPath: string, title: string, workspace?: string | null) => {
      const current = stateRef.current;
      const normalizedWorkspace = workspace ?? null;
      const existingTab = current.tabs.find(
        (t) =>
          t.docPath === docPath && (t.workspace ?? null) === normalizedWorkspace
      );
      if (existingTab) {
        commitState({ ...current, activeTabId: existingTab.id });
        return;
      }

      const newTab: DocTab = {
        id: generateTabId(),
        docPath,
        title,
        workspace: normalizedWorkspace,
      };
      commitState({
        ...current,
        tabs: [...current.tabs, newTab],
        activeTabId: newTab.id,
      });
    },
    [commitState]
  );

  const closeTab = useCallback(
    (tabId: string) => {
      const current = stateRef.current;
      const nextTabs = current.tabs.filter((tab) => tab.id !== tabId);
      let nextActiveTabId = current.activeTabId;
      if (current.activeTabId === tabId) {
        const closedIndex = current.tabs.findIndex((tab) => tab.id === tabId);
        const nextActiveIndex = Math.min(closedIndex, nextTabs.length - 1);
        nextActiveTabId = nextTabs[nextActiveIndex]?.id ?? null;
      } else if (nextTabs.length === 0) {
        nextActiveTabId = null;
      }

      const nextDrafts = new Map(current.drafts);
      for (const key of nextDrafts.keys()) {
        if (draftKeyMatchesTabId(key, tabId)) {
          nextDrafts.delete(key);
        }
      }
      const nextUnsavedTabIds = new Set(current.unsavedTabIds);
      nextUnsavedTabIds.delete(tabId);
      commitState({
        tabs: nextTabs,
        activeTabId: nextActiveTabId,
        drafts: nextDrafts,
        unsavedTabIds: nextUnsavedTabIds,
      });
    },
    [commitState]
  );

  const closeAllTabs = useCallback(() => {
    commitState({
      tabs: [],
      activeTabId: null,
      drafts: new Map(),
      unsavedTabIds: new Set(),
    });
  }, [commitState]);

  const closeOtherTabs = useCallback(
    (keepTabId: string) => {
      const current = stateRef.current;
      const nextTabs = current.tabs.filter((tab) => tab.id === keepTabId);
      const nextDrafts = new Map<string, string>();
      for (const [key, value] of current.drafts) {
        if (draftKeyMatchesTabId(key, keepTabId)) {
          nextDrafts.set(key, value);
        }
      }
      const nextUnsavedTabIds = new Set<string>();
      if (current.unsavedTabIds.has(keepTabId)) {
        nextUnsavedTabIds.add(keepTabId);
      }
      commitState({
        tabs: nextTabs,
        activeTabId: keepTabId,
        drafts: nextDrafts,
        unsavedTabIds: nextUnsavedTabIds,
      });
    },
    [commitState]
  );

  const setActiveTab = useCallback(
    (tabId: string) => {
      commitState({ ...stateRef.current, activeTabId: tabId });
    },
    [commitState]
  );

  const getActiveDocPath = useCallback(() => {
    const current = stateRef.current;
    if (!current.activeTabId) return null;
    const activeTab = current.tabs.find(
      (tab) => tab.id === current.activeTabId
    );
    return activeTab?.docPath || null;
  }, []);

  const updateTab = useCallback(
    (
      tabId: string,
      updates: Partial<Pick<DocTab, 'docPath' | 'title' | 'workspace'>>
    ) => {
      const current = stateRef.current;
      const nextTabs = current.tabs.map((tab) =>
        tab.id === tabId ? { ...tab, ...updates } : tab
      );
      commitState({ ...current, tabs: nextTabs });
    },
    [commitState]
  );

  const setDraft = useCallback(
    (draftKey: string, content: string) => {
      const current = stateRef.current;
      const matchingTab = current.tabs.find((tab) =>
        draftKeyMatchesTabId(draftKey, tab.id)
      );
      if (!matchingTab) return;

      const nextDrafts = new Map(current.drafts);
      nextDrafts.set(draftKey, content);
      let nextUnsavedTabIds = current.unsavedTabIds;
      if (!nextUnsavedTabIds.has(matchingTab.id)) {
        nextUnsavedTabIds = new Set(nextUnsavedTabIds);
        nextUnsavedTabIds.add(matchingTab.id);
      }
      commitState({
        ...current,
        drafts: nextDrafts,
        unsavedTabIds: nextUnsavedTabIds,
      });
    },
    [commitState]
  );

  const clearDraft = useCallback(
    (draftKey: string) => {
      const current = stateRef.current;
      const tabId = draftTabIdFromKey(draftKey);
      const nextDrafts = new Map(current.drafts);
      for (const key of nextDrafts.keys()) {
        if (draftKeyMatchesTabId(key, tabId)) {
          nextDrafts.delete(key);
        }
      }
      commitState({ ...current, drafts: nextDrafts });
    },
    [commitState]
  );

  const getDraft = useCallback((draftKey: string) => {
    const drafts = stateRef.current.drafts;
    const exactDraft = drafts.get(draftKey);
    if (exactDraft !== undefined) return exactDraft;

    const tabId = draftTabIdFromKey(draftKey);
    const directDraft = drafts.get(tabId);
    if (directDraft !== undefined) return directDraft;

    for (const [key, draft] of drafts) {
      if (draftKeyMatchesTabId(key, tabId)) return draft;
    }
    return undefined;
  }, []);

  const markTabUnsaved = useCallback(
    (tabId: string) => {
      const current = stateRef.current;
      if (current.unsavedTabIds.has(tabId)) return;
      const nextUnsavedTabIds = new Set(current.unsavedTabIds);
      nextUnsavedTabIds.add(tabId);
      commitState({ ...current, unsavedTabIds: nextUnsavedTabIds });
    },
    [commitState]
  );

  const markTabSaved = useCallback(
    (tabId: string) => {
      const current = stateRef.current;
      if (!current.unsavedTabIds.has(tabId)) return;
      const nextUnsavedTabIds = new Set(current.unsavedTabIds);
      nextUnsavedTabIds.delete(tabId);
      commitState({ ...current, unsavedTabIds: nextUnsavedTabIds });
    },
    [commitState]
  );

  const isTabUnsaved = useCallback((tabId: string) => {
    return stateRef.current.unsavedTabIds.has(tabId);
  }, []);

  const value: DocTabContextType = {
    tabs: state.tabs,
    activeTabId: state.activeTabId,
    openDoc,
    closeTab,
    closeAllTabs,
    closeOtherTabs,
    setActiveTab,
    getActiveDocPath,
    updateTab,
    drafts: state.drafts,
    setDraft,
    clearDraft,
    getDraft,
    unsavedTabIds: state.unsavedTabIds,
    markTabUnsaved,
    markTabSaved,
    isTabUnsaved,
  };

  return (
    <DocTabContext.Provider value={value}>{children}</DocTabContext.Provider>
  );
}
