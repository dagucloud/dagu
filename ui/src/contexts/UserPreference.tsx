import React, { createContext, useCallback, useContext, useState } from 'react';
import { writeLocalStorage } from '@/lib/local-storage-migration';

export type DAGRunsViewMode = 'list' | 'grouped';
export type Locale = 'en' | 'zh-CN' | 'ja';

export type WikiSortField = 'name' | 'type' | 'mtime';
export type WikiSortOrder = 'asc' | 'desc';

export type UserPreferences = {
  pageLimit: number;
  dagRunsViewMode: DAGRunsViewMode;
  logWrap: boolean;
  theme: 'light' | 'dark';
  locale: Locale;
  safeMode: boolean;
  wikiSortField: WikiSortField;
  wikiSortOrder: WikiSortOrder;
};

const UserPreferencesContext = createContext<{
  preferences: UserPreferences;
  updatePreference: <K extends keyof UserPreferences>(
    key: K,
    value: UserPreferences[K]
  ) => void;
}>(null!);

const defaultPreferences: UserPreferences = {
  pageLimit: 50,
  dagRunsViewMode: 'list',
  logWrap: true,
  theme: 'light', // Default to light theme (from main branch)
  locale: 'en',
  safeMode: false,
  wikiSortField: 'type',
  wikiSortOrder: 'asc',
};

function isWikiSortField(value: unknown): value is WikiSortField {
  return value === 'name' || value === 'type' || value === 'mtime';
}

function isWikiSortOrder(value: unknown): value is WikiSortOrder {
  return value === 'asc' || value === 'desc';
}

function isLocale(value: unknown): value is Locale {
  return value === 'en' || value === 'zh-CN' || value === 'ja';
}

function loadPreferences(): UserPreferences {
  try {
    const saved = localStorage.getItem('user_preferences');
    if (!saved) {
      return defaultPreferences;
    }
    const preferences = JSON.parse(saved) as Record<string, unknown>;
    delete preferences.workflowFilterViews;
    const migrated = {
      ...defaultPreferences,
      ...preferences,
      locale: isLocale(preferences.locale)
        ? preferences.locale
        : defaultPreferences.locale,
      wikiSortField: isWikiSortField(preferences.wikiSortField)
        ? preferences.wikiSortField
        : isWikiSortField(preferences.docSortField)
          ? preferences.docSortField
          : defaultPreferences.wikiSortField,
      wikiSortOrder: isWikiSortOrder(preferences.wikiSortOrder)
        ? preferences.wikiSortOrder
        : isWikiSortOrder(preferences.docSortOrder)
          ? preferences.docSortOrder
          : defaultPreferences.wikiSortOrder,
    } as UserPreferences;
    writeLocalStorage(
      'user_preferences',
      JSON.stringify({ ...preferences, ...migrated })
    );
    return migrated;
  } catch {
    return defaultPreferences;
  }
}

export function UserPreferencesProvider({
  children,
}: {
  children: React.ReactNode;
}) {
  const [preferences, setPreferences] =
    useState<UserPreferences>(loadPreferences);

  const updatePreference = useCallback(
    <K extends keyof UserPreferences>(key: K, value: UserPreferences[K]) => {
      setPreferences((prev) => {
        const next = { ...prev, [key]: value };
        localStorage.setItem('user_preferences', JSON.stringify(next));
        return next;
      });
    },
    []
  );

  return (
    <UserPreferencesContext.Provider value={{ preferences, updatePreference }}>
      {children}
    </UserPreferencesContext.Provider>
  );
}

export function useUserPreferences() {
  return useContext(UserPreferencesContext);
}
