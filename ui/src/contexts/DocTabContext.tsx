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
  const draftsRef = useRef(drafts);
  draftsRef.current = drafts;
  const unsavedTabIdsRef = useRef(unsavedTabIds);
  unsavedTabIdsRef.current = unsavedTabIds;

  const persistState = useCallback(
    (overrides?: Partial<StoredTabState>) => {
      writeStoredTabState(storageKey, {
        tabs: tabsRef.current,
        activeTabId: activeTabIdRef.current,
        drafts: Array.from(draftsRef.current.entries()),
        unsavedTabIds: Array.from(unsavedTabIdsRef.current),
        ...overrides,
      });
    },
    [storageKey]
  );

  // Persist to localStorage
  useEffect(() => {
    persistState();
  }, [tabs, activeTabId, drafts, unsavedTabIds, persistState]);

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
      setTabs((prev) => [...prev, newTab]);
      setActiveTabId(newTab.id);
    },
    []
  );

  const closeTab = useCallback(
    (tabId: string) => {
      setTabs((prev) => {
        const newTabs = prev.filter((t) => t.id !== tabId);

        if (activeTabIdRef.current === tabId && newTabs.length > 0) {
          const closedIndex = prev.findIndex((t) => t.id === tabId);
          const newActiveIndex = Math.min(closedIndex, newTabs.length - 1);
          setActiveTabId(newTabs[newActiveIndex]?.id || null);
        } else if (newTabs.length === 0) {
          setActiveTabId(null);
        }

        return newTabs;
      });

      // Clear draft and unsaved state for closed tab
      setDrafts((prev) => {
        const next = new Map(prev);
        for (const key of next.keys()) {
          if (draftKeyMatchesTabId(key, tabId)) {
            next.delete(key);
          }
        }
        draftsRef.current = next;
        persistState({ drafts: Array.from(next.entries()) });
        return next;
      });
      setUnsavedTabIds((prev) => {
        const next = new Set(prev);
        next.delete(tabId);
        unsavedTabIdsRef.current = next;
        persistState({ unsavedTabIds: Array.from(next) });
        return next;
      });
    },
    [persistState]
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
    persistState({
      tabs: [],
      activeTabId: null,
      drafts: [],
      unsavedTabIds: [],
    });
  }, [persistState]);

  const closeOtherTabs = useCallback(
    (keepTabId: string) => {
      setTabs((prev) => prev.filter((t) => t.id === keepTabId));
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
        draftsRef.current = next;
        persistState({ drafts: Array.from(next.entries()) });
        return next;
      });
      setUnsavedTabIds((prev) => {
        const next = new Set<string>();
        if (prev.has(keepTabId)) next.add(keepTabId);
        unsavedTabIdsRef.current = next;
        persistState({ unsavedTabIds: Array.from(next) });
        return next;
      });
    },
    [persistState]
  );

  const setActiveTab = useCallback((tabId: string) => {
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
      setTabs((prev) =>
        prev.map((t) => (t.id === tabId ? { ...t, ...updates } : t))
      );
    },
    []
  );

  const setDraft = useCallback(
    (tabId: string, content: string) => {
      setDrafts((prev) => {
        const next = new Map(prev);
        next.set(tabId, content);
        draftsRef.current = next;
        persistState({ drafts: Array.from(next.entries()) });
        return next;
      });
    },
    [persistState]
  );

  const clearDraft = useCallback(
    (tabId: string) => {
      setDrafts((prev) => {
        const next = new Map(prev);
        for (const key of next.keys()) {
          if (draftKeyMatchesTabId(key, tabId)) {
            next.delete(key);
          }
        }
        draftsRef.current = next;
        persistState({ drafts: Array.from(next.entries()) });
        return next;
      });
    },
    [persistState]
  );

  const getDraft = useCallback(
    (tabId: string) => {
      return drafts.get(tabId);
    },
    [drafts]
  );

  const markTabUnsaved = useCallback(
    (tabId: string) => {
      setUnsavedTabIds((prev) => {
        if (prev.has(tabId)) return prev;
        const next = new Set(prev);
        next.add(tabId);
        unsavedTabIdsRef.current = next;
        persistState({ unsavedTabIds: Array.from(next) });
        return next;
      });
    },
    [persistState]
  );

  const markTabSaved = useCallback(
    (tabId: string) => {
      setUnsavedTabIds((prev) => {
        if (!prev.has(tabId)) return prev;
        const next = new Set(prev);
        next.delete(tabId);
        unsavedTabIdsRef.current = next;
        persistState({ unsavedTabIds: Array.from(next) });
        return next;
      });
    },
    [persistState]
  );

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
