import React, { createContext, useCallback, useContext, useState } from 'react';

export type DAGRunsViewMode = 'list' | 'grouped';

export type WikiSortField = 'name' | 'type' | 'mtime';
export type WikiSortOrder = 'asc' | 'desc';

export type UserPreferences = {
  pageLimit: number;
  dagRunsViewMode: DAGRunsViewMode;
  logWrap: boolean;
  theme: 'light' | 'dark';
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
  safeMode: false,
  wikiSortField: 'type',
  wikiSortOrder: 'asc',
};

function loadPreferences(): UserPreferences {
  try {
    const saved = localStorage.getItem('user_preferences');
    if (!saved) {
      return defaultPreferences;
    }
    const preferences = JSON.parse(saved) as Record<string, unknown>;
    if (
      preferences.wikiSortField === undefined &&
      preferences.docSortField !== undefined
    ) {
      preferences.wikiSortField = preferences.docSortField;
    }
    if (
      preferences.wikiSortOrder === undefined &&
      preferences.docSortOrder !== undefined
    ) {
      preferences.wikiSortOrder = preferences.docSortOrder;
    }
    delete preferences.workflowFilterViews;
    const migrated = {
      ...defaultPreferences,
      ...preferences,
    } as UserPreferences;
    localStorage.setItem(
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
