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
    const stored =
      localStorage.getItem(storageKey) ||
      (storageKey !== STORAGE_KEY ? localStorage.getItem(STORAGE_KEY) : null);
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

function draftKeyMatchesTabId(key: string, tabId: string): boolean {
  if (key === tabId) return true;
  try {
    const parsed = JSON.parse(key) as { tabId?: unknown };
    return parsed.tabId === tabId;
  } catch {
    return false;
  }
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
    },
    []
  );

  const closeTab = useCallback((tabId: string) => {
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

    setDrafts((prev) => {
      const next = new Map(prev);
      for (const key of next.keys()) {
        if (draftKeyMatchesTabId(key, tabId)) {
          next.delete(key);
        }
      }
      return next;
    });
    setUnsavedTabIds((prev) => {
      const next = new Set(prev);
      next.delete(tabId);
      return next;
    });
  }, []);

  const closeAllTabs = useCallback(() => {
    setTabs([]);
    setActiveTabId(null);
    setDrafts(new Map());
    setUnsavedTabIds(new Set());
    tabsRef.current = [];
    activeTabIdRef.current = null;
  }, []);

  const closeOtherTabs = useCallback((keepTabId: string) => {
    const nextTabs = tabsRef.current.filter((t) => t.id === keepTabId);
    tabsRef.current = nextTabs;
    activeTabIdRef.current = keepTabId;
    setTabs(nextTabs);
    setActiveTabId(keepTabId);
    setDrafts((prev) => {
      const draft = prev.get(keepTabId);
      const next = new Map<string, string>();
      if (draft !== undefined) next.set(keepTabId, draft);
      for (const [key, value] of prev.entries()) {
        if (draftKeyMatchesTabId(key, keepTabId)) {
          next.set(key, value);
        }
      }
      return next;
    });
    setUnsavedTabIds((prev) => {
      const next = new Set<string>();
      if (prev.has(keepTabId)) next.add(keepTabId);
      return next;
    });
  }, []);

  const setActiveTab = useCallback((tabId: string) => {
    activeTabIdRef.current = tabId;
    setActiveTabId(tabId);
  }, []);

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
    },
    []
  );

  const setDraft = useCallback((draftKey: string, content: string) => {
    const matchesOpenTab = tabsRef.current.some((tab) =>
      draftKeyMatchesTabId(draftKey, tab.id)
    );
    if (!matchesOpenTab) return;

    setDrafts((prev) => {
      const next = new Map(prev);
      next.set(draftKey, content);
      return next;
    });
  }, []);

  const clearDraft = useCallback((tabId: string) => {
    setDrafts((prev) => {
      const next = new Map(prev);
      for (const key of next.keys()) {
        if (draftKeyMatchesTabId(key, tabId)) {
          next.delete(key);
        }
      }
      return next;
    });
  }, []);

  const getDraft = useCallback(
    (tabId: string) => {
      return drafts.get(tabId);
    },
    [drafts]
  );

  const markTabUnsaved = useCallback((tabId: string) => {
    setUnsavedTabIds((prev) => {
      if (prev.has(tabId)) return prev;
      const next = new Set(prev);
      next.add(tabId);
      return next;
    });
  }, []);

  const markTabSaved = useCallback((tabId: string) => {
    setUnsavedTabIds((prev) => {
      if (!prev.has(tabId)) return prev;
      const next = new Set(prev);
      next.delete(tabId);
      return next;
    });
  }, []);

  const isTabUnsaved = useCallback(
    (tabId: string) => {
      return unsavedTabIds.has(tabId);
    },
    [unsavedTabIds]
  );

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
