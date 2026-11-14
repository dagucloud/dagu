import React, {
  createContext,
  useCallback,
  useContext,
  useState,
  useSyncExternalStore,
} from 'react';
import { writeLocalStorage } from '@/lib/local-storage-migration';

export type DAGRunsViewMode = 'list' | 'grouped';
export type Locale = 'en' | 'zh-CN' | 'ja';

/** Which end of a log is at the top of the viewer. */
export type LogOrder = 'oldToNew' | 'newToOld';

export type WikiSortField = 'name' | 'type' | 'mtime';
export type WikiSortOrder = 'asc' | 'desc';
/** The chosen color theme; `system` follows the operating system. */
export type ThemePreference = 'light' | 'dark' | 'system';
export type ResolvedTheme = 'light' | 'dark';

export type UserPreferences = {
  pageLimit: number;
  dagRunsViewMode: DAGRunsViewMode;
  logWrap: boolean;
  logOrder: LogOrder;
  theme: ThemePreference;
  locale: Locale;
  safeMode: boolean;
  wikiSortField: WikiSortField;
  wikiSortOrder: WikiSortOrder;
  pinnedViewOrder: Record<string, string[]>;
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
  logOrder: 'oldToNew',
  theme: 'system',
  locale: 'en',
  safeMode: false,
  wikiSortField: 'type',
  wikiSortOrder: 'asc',
  pinnedViewOrder: {},
};

function isWikiSortField(value: unknown): value is WikiSortField {
  return value === 'name' || value === 'type' || value === 'mtime';
}

function isWikiSortOrder(value: unknown): value is WikiSortOrder {
  return value === 'asc' || value === 'desc';
}

function isLogOrder(value: unknown): value is LogOrder {
  return value === 'oldToNew' || value === 'newToOld';
}

function isLocale(value: unknown): value is Locale {
  return value === 'en' || value === 'zh-CN' || value === 'ja';
}

function isThemePreference(value: unknown): value is ThemePreference {
  return value === 'light' || value === 'dark' || value === 'system';
}

function loadPreferences(): UserPreferences {
  try {
    const saved = localStorage.getItem('user_preferences');
    if (!saved) {
      return defaultPreferences;
    }
    const preferences = JSON.parse(saved) as Record<string, unknown>;
    delete preferences.workflowFilterViews;
    // The spec tab has one view; an old choice of another is dropped.
    delete preferences.specViewMode;
    // The workflow page has one body; an old choice of another is dropped.
    delete preferences.workflowPage;
    const migrated = {
      ...defaultPreferences,
      ...preferences,
      logOrder: isLogOrder(preferences.logOrder)
        ? preferences.logOrder
        : defaultPreferences.logOrder,
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
      theme: isThemePreference(preferences.theme)
        ? preferences.theme
        : defaultPreferences.theme,
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
        writeLocalStorage('user_preferences', JSON.stringify(next));
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

const DARK_COLOR_SCHEME_QUERY = '(prefers-color-scheme: dark)';

function subscribeToColorScheme(onChange: () => void): () => void {
  const query = window.matchMedia?.(DARK_COLOR_SCHEME_QUERY);
  query?.addEventListener('change', onChange);
  return () => query?.removeEventListener('change', onChange);
}

function systemPrefersDark(): boolean {
  return window.matchMedia?.(DARK_COLOR_SCHEME_QUERY).matches ?? false;
}

/**
 * Returns the theme in effect: the user's choice, or the operating system's
 * setting when the choice is `system`. Updates when either changes.
 */
export function useResolvedTheme(): ResolvedTheme {
  const { preferences } = useUserPreferences();
  const prefersDark = useSyncExternalStore(
    subscribeToColorScheme,
    systemPrefersDark,
    () => false
  );
  if (preferences.theme === 'system') {
    return prefersDark ? 'dark' : 'light';
  }
  return preferences.theme;
}
