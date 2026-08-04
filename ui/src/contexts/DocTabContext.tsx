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

  const [tabs, setTabs] = useState<DocTab[]>(() => {
    return readStoredTabState(storageKey)?.tabs || [];
  });

  const [activeTabId, setActiveTabId] = useState<string | null>(() => {
    const parsed = readStoredTabState(storageKey);
    if (
      parsed?.activeTabId &&
      parsed.tabs?.some((t) => t.id === parsed.activeTabId)
    ) {
      return parsed.activeTabId;
    }
    return null;
  });

  const [drafts, setDrafts] = useState<Map<string, string>>(() => {
    return new Map(readStoredTabState(storageKey)?.drafts ?? []);
  });
  const [unsavedTabIds, setUnsavedTabIds] = useState<Set<string>>(() => {
    return new Set(readStoredTabState(storageKey)?.unsavedTabIds ?? []);
  });

  // Use ref to track tabs for use in callbacks without stale closures
  const tabsRef = useRef(tabs);
  tabsRef.current = tabs;
  const activeTabIdRef = useRef(activeTabId);
  activeTabIdRef.current = activeTabId;
  const draftsRef = useRef(drafts);
  draftsRef.current = drafts;
  const unsavedTabIdsRef = useRef(unsavedTabIds);
  unsavedTabIdsRef.current = unsavedTabIds;

  const persistStoredState = useCallback(() => {
    writeStoredTabState(storageKey, {
      tabs: tabsRef.current,
      activeTabId: activeTabIdRef.current,
      drafts: Array.from(draftsRef.current.entries()),
      unsavedTabIds: Array.from(unsavedTabIdsRef.current),
    });
  }, [storageKey]);

  // Persist to localStorage
  useEffect(() => {
    writeStoredTabState(storageKey, {
      tabs,
      activeTabId,
      drafts: Array.from(drafts.entries()),
      unsavedTabIds: Array.from(unsavedTabIds),
    });
  }, [storageKey, tabs, activeTabId, drafts, unsavedTabIds]);

  // Sync unsavedTabIds to UnsavedChangesContext
  useEffect(() => {
    setHasUnsavedChanges(unsavedTabIds.size > 0);
    return () => {
      setHasUnsavedChanges(false);
    };
  }, [unsavedTabIds, setHasUnsavedChanges]);

  const openDoc = useCallback(
    (docPath: string, title: string, workspace?: string | null) => {
      // Check if already open
      const normalizedWorkspace = workspace ?? null;
      const existingTab = tabsRef.current.find(
        (t) =>
          t.docPath === docPath && (t.workspace ?? null) === normalizedWorkspace
      );
      if (existingTab) {
        activeTabIdRef.current = existingTab.id;
        setActiveTabId(existingTab.id);
        persistStoredState();
        return;
      }

      // Create new tab
      const newTab: DocTab = {
        id: generateTabId(),
        docPath,
        title,
        workspace: normalizedWorkspace,
      };
      const nextTabs = [...tabsRef.current, newTab];
      tabsRef.current = nextTabs;
      activeTabIdRef.current = newTab.id;
      setTabs(nextTabs);
      setActiveTabId(newTab.id);
      persistStoredState();
    },
    [persistStoredState]
  );

  const closeTab = useCallback(
    (tabId: string) => {
      const currentTabs = tabsRef.current;
      const nextTabs = currentTabs.filter((t) => t.id !== tabId);
      tabsRef.current = nextTabs;
      setTabs(nextTabs);

      if (activeTabIdRef.current === tabId) {
        const closedIndex = currentTabs.findIndex((t) => t.id === tabId);
        const nextActiveIndex = Math.min(closedIndex, nextTabs.length - 1);
        const nextActiveTabId = nextTabs[nextActiveIndex]?.id ?? null;
        activeTabIdRef.current = nextActiveTabId;
        setActiveTabId(nextActiveTabId);
      } else if (nextTabs.length === 0) {
        activeTabIdRef.current = null;
        setActiveTabId(null);
      }

      const nextDrafts = new Map(draftsRef.current);
      for (const key of nextDrafts.keys()) {
        if (draftKeyMatchesTabId(key, tabId)) {
          nextDrafts.delete(key);
        }
      }
      draftsRef.current = nextDrafts;
      setDrafts(nextDrafts);

      const nextUnsavedTabIds = new Set(unsavedTabIdsRef.current);
      nextUnsavedTabIds.delete(tabId);
      unsavedTabIdsRef.current = nextUnsavedTabIds;
      setUnsavedTabIds(nextUnsavedTabIds);
      persistStoredState();
    },
    [persistStoredState]
  );

  const closeAllTabs = useCallback(() => {
    setTabs([]);
    setActiveTabId(null);
    setDrafts(new Map());
    setUnsavedTabIds(new Set());
    tabsRef.current = [];
    activeTabIdRef.current = null;
    draftsRef.current = new Map();
    unsavedTabIdsRef.current = new Set();
    persistStoredState();
  }, [persistStoredState]);

  const closeOtherTabs = useCallback(
    (keepTabId: string) => {
      const nextTabs = tabsRef.current.filter((t) => t.id === keepTabId);
      tabsRef.current = nextTabs;
      activeTabIdRef.current = keepTabId;
      setTabs(nextTabs);
      setActiveTabId(keepTabId);
      const nextDrafts = new Map<string, string>();
      for (const [key, value] of draftsRef.current.entries()) {
        if (draftKeyMatchesTabId(key, keepTabId)) {
          nextDrafts.set(key, value);
        }
      }
      draftsRef.current = nextDrafts;
      setDrafts(nextDrafts);

      const nextUnsavedTabIds = new Set<string>();
      if (unsavedTabIdsRef.current.has(keepTabId)) {
        nextUnsavedTabIds.add(keepTabId);
      }
      unsavedTabIdsRef.current = nextUnsavedTabIds;
      setUnsavedTabIds(nextUnsavedTabIds);
      persistStoredState();
    },
    [persistStoredState]
  );

  const setActiveTab = useCallback(
    (tabId: string) => {
      activeTabIdRef.current = tabId;
      setActiveTabId(tabId);
      persistStoredState();
    },
    [persistStoredState]
  );

  const getActiveDocPath = useCallback(() => {
    if (!activeTabIdRef.current) return null;
    const activeTab = tabsRef.current.find(
      (t) => t.id === activeTabIdRef.current
    );
    return activeTab?.docPath || null;
  }, []);

  const updateTab = useCallback(
    (
      tabId: string,
      updates: Partial<Pick<DocTab, 'docPath' | 'title' | 'workspace'>>
    ) => {
      const nextTabs = tabsRef.current.map((tab) =>
        tab.id === tabId ? { ...tab, ...updates } : tab
      );
      tabsRef.current = nextTabs;
      setTabs(nextTabs);
      persistStoredState();
    },
    [persistStoredState]
  );

  const setDraft = useCallback(
    (draftKey: string, content: string) => {
      const matchingTab = tabsRef.current.find((tab) =>
        draftKeyMatchesTabId(draftKey, tab.id)
      );
      if (!matchingTab) return;

      const nextDrafts = new Map(draftsRef.current);
      nextDrafts.set(draftKey, content);
      draftsRef.current = nextDrafts;
      setDrafts(nextDrafts);

      if (!unsavedTabIdsRef.current.has(matchingTab.id)) {
        const nextUnsavedTabIds = new Set(unsavedTabIdsRef.current);
        nextUnsavedTabIds.add(matchingTab.id);
        unsavedTabIdsRef.current = nextUnsavedTabIds;
        setUnsavedTabIds(nextUnsavedTabIds);
      }
      persistStoredState();
    },
    [persistStoredState]
  );

  const clearDraft = useCallback(
    (draftKey: string) => {
      const tabId = draftTabIdFromKey(draftKey);
      const nextDrafts = new Map(draftsRef.current);
      for (const key of nextDrafts.keys()) {
        if (draftKeyMatchesTabId(key, tabId)) {
          nextDrafts.delete(key);
        }
      }
      draftsRef.current = nextDrafts;
      setDrafts(nextDrafts);
      persistStoredState();
    },
    [persistStoredState]
  );

  const getDraft = useCallback((draftKey: string) => {
    const exactDraft = draftsRef.current.get(draftKey);
    if (exactDraft !== undefined) return exactDraft;

    const tabId = draftTabIdFromKey(draftKey);
    const directDraft = draftsRef.current.get(tabId);
    if (directDraft !== undefined) return directDraft;

    for (const [key, draft] of draftsRef.current) {
      if (draftKeyMatchesTabId(key, tabId)) return draft;
    }
    return undefined;
  }, []);

  const markTabUnsaved = useCallback(
    (tabId: string) => {
      if (unsavedTabIdsRef.current.has(tabId)) return;
      const nextUnsavedTabIds = new Set(unsavedTabIdsRef.current);
      nextUnsavedTabIds.add(tabId);
      unsavedTabIdsRef.current = nextUnsavedTabIds;
      setUnsavedTabIds(nextUnsavedTabIds);
      persistStoredState();
    },
    [persistStoredState]
  );

  const markTabSaved = useCallback(
    (tabId: string) => {
      if (!unsavedTabIdsRef.current.has(tabId)) return;
      const nextUnsavedTabIds = new Set(unsavedTabIdsRef.current);
      nextUnsavedTabIds.delete(tabId);
      unsavedTabIdsRef.current = nextUnsavedTabIds;
      setUnsavedTabIds(nextUnsavedTabIds);
      persistStoredState();
    },
    [persistStoredState]
  );

  const isTabUnsaved = useCallback((tabId: string) => {
    return unsavedTabIdsRef.current.has(tabId);
  }, []);

  const value: DocTabContextType = {
    tabs,
    activeTabId,
    openDoc,
    closeTab,
    closeAllTabs,
    closeOtherTabs,
    setActiveTab,
    getActiveDocPath,
    updateTab,
    drafts,
    setDraft,
    clearDraft,
    getDraft,
    unsavedTabIds,
    markTabUnsaved,
    markTabSaved,
    isTabUnsaved,
  };

  return (
    <DocTabContext.Provider value={value}>{children}</DocTabContext.Provider>
  );
}
