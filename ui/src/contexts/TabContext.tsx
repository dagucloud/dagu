// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useState,
} from 'react';

export interface Tab {
  id: string;
  fileName: string;
  title: string;
}

interface TabContextType {
  tabs: Tab[];
  activeTabId: string | null;
  addTab: (fileName: string, title: string) => void;
  closeTab: (tabId: string) => void;
  closeTabByFileName: (fileName: string) => void;
  setActiveTab: (tabId: string) => void;
  selectDAG: (fileName: string, title: string) => void;
  getActiveFileName: () => string | null;
}

const STORAGE_KEY = 'dagu_dag_tabs';

const TabContext = createContext<TabContextType | null>(null);

export function useTabContext() {
  const context = useContext(TabContext);
  if (!context) {
    throw new Error('useTabContext must be used within a TabProvider');
  }
  return context;
}

// Optional hook that returns null if not within provider
export function useOptionalTabContext() {
  return useContext(TabContext);
}

interface StoredTabState {
  tabs: Tab[];
  activeTabId: string | null;
}

function generateTabId(): string {
  return `tab-${Date.now()}-${Math.random().toString(36).substring(2, 11)}`;
}

function readStoredTabState(storageKey: string): StoredTabState | null {
  try {
    const stored = localStorage.getItem(storageKey);
    if (stored) {
      const parsed = JSON.parse(stored) as Partial<StoredTabState>;
      if (Array.isArray(parsed.tabs)) {
        return {
          tabs: parsed.tabs,
          activeTabId:
            typeof parsed.activeTabId === 'string'
              ? parsed.activeTabId
              : null,
        };
      }
    }
  } catch {
    // Ignore parse errors
  }
  return null;
}

export function TabProvider({
  children,
  storageKey = STORAGE_KEY,
}: {
  children: React.ReactNode;
  storageKey?: string;
}) {
  const [tabs, setTabs] = useState<Tab[]>(() => {
    return readStoredTabState(storageKey)?.tabs || [];
  });

  const [activeTabId, setActiveTabId] = useState<string | null>(() => {
    const parsed = readStoredTabState(storageKey);
    // Validate that activeTabId exists in tabs
    if (
      parsed?.activeTabId &&
      parsed.tabs?.some((t) => t.id === parsed.activeTabId)
    ) {
      return parsed.activeTabId;
    }
    return null;
  });

  // Persist to localStorage
  useEffect(() => {
    try {
      const state: StoredTabState = { tabs, activeTabId };
      localStorage.setItem(storageKey, JSON.stringify(state));
    } catch {
      // Ignore persistence errors (quota/private mode)
    }
  }, [tabs, activeTabId, storageKey]);

  const addTab = useCallback((fileName: string, title: string) => {
    const newTab: Tab = {
      id: generateTabId(),
      fileName,
      title,
    };
    setTabs((prev) => [...prev, newTab]);
    setActiveTabId(newTab.id);
  }, []);

  const closeTab = useCallback(
    (tabId: string) => {
      setTabs((prev) => {
        const newTabs = prev.filter((t) => t.id !== tabId);

        // If we're closing the active tab, switch to another
        if (activeTabId === tabId && newTabs.length > 0) {
          const closedIndex = prev.findIndex((t) => t.id === tabId);
          const newActiveIndex = Math.min(closedIndex, newTabs.length - 1);
          setActiveTabId(newTabs[newActiveIndex]?.id || null);
        } else if (newTabs.length === 0) {
          setActiveTabId(null);
        }

        return newTabs;
      });
    },
    [activeTabId]
  );

  const closeTabByFileName = useCallback(
    (fileName: string) => {
      const tab = tabs.find((t) => t.fileName === fileName);
      if (tab) {
        closeTab(tab.id);
      }
    },
    [tabs, closeTab]
  );

  const setActiveTab = useCallback((tabId: string) => {
    setActiveTabId(tabId);
  }, []);

  // Main function for selecting a DAG - reuses active tab or creates first tab
  const selectDAG = useCallback(
    (fileName: string, title: string) => {
      // Check if this DAG is already open in a tab
      const existingTab = tabs.find((t) => t.fileName === fileName);
      if (existingTab) {
        setActiveTabId(existingTab.id);
        return;
      }

      // If there's an active tab, reuse it
      if (activeTabId) {
        setTabs((prev) =>
          prev.map((t) =>
            t.id === activeTabId ? { ...t, fileName, title } : t
          )
        );
      } else {
        // No tabs exist, create first one
        addTab(fileName, title);
      }
    },
    [tabs, activeTabId, addTab]
  );

  const getActiveFileName = useCallback(() => {
    if (!activeTabId) return null;
    const activeTab = tabs.find((t) => t.id === activeTabId);
    return activeTab?.fileName || null;
  }, [tabs, activeTabId]);

  const value: TabContextType = {
    tabs,
    activeTabId,
    addTab,
    closeTab,
    closeTabByFileName,
    setActiveTab,
    selectDAG,
    getActiveFileName,
  };

  return <TabContext.Provider value={value}>{children}</TabContext.Provider>;
}
