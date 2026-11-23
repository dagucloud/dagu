import React from 'react';

type StoredSearchStates = Record<string, unknown>;

type SearchStateContextValue = {
  readState<T>(pageKey: string, remoteKey?: string): T | undefined;
  writeState<T>(pageKey: string, remoteKey: string | undefined, value: T): void;
  resetState(pageKey: string, remoteKey?: string): void;
};

const STORAGE_KEY = 'dagu.searchState';

const SearchStateContext = React.createContext<SearchStateContextValue | null>(
  null
);

const getInitialStore = (): StoredSearchStates => {
  if (typeof window === 'undefined') {
    return {};
  }

  try {
    const raw = window.sessionStorage.getItem(STORAGE_KEY);
    if (!raw) {
      return {};
    }
    const parsed = JSON.parse(raw);
    if (parsed && typeof parsed === 'object') {
      return parsed as StoredSearchStates;
    }
  } catch (error) {
    console.warn('Failed to read search state from sessionStorage', error);
  }

  return {};
};

const buildKey = (pageKey: string, remoteKey?: string) =>
  remoteKey ? `${pageKey}:${remoteKey}` : pageKey;

const valuesAreEqual = (a: unknown, b: unknown) => {
  if (a === b) {
    return true;
  }

  try {
    return JSON.stringify(a) === JSON.stringify(b);
  } catch {
    return false;
  }
};

export function SearchStateProvider({
  children,
}: {
  children: React.ReactNode;
}) {
  const [store, setStore] = React.useState<StoredSearchStates>(() => {
    const initial = getInitialStore();
    return initial;
  });
  const storeRef = React.useRef<StoredSearchStates>(store);

  const persistStore = React.useCallback((next: StoredSearchStates) => {
    if (typeof window === 'undefined') {
      return;
    }

    try {
      window.sessionStorage.setItem(STORAGE_KEY, JSON.stringify(next));
    } catch (error) {
      console.warn('Failed to persist search state to sessionStorage', error);
    }
  }, []);

  const readState = React.useCallback(<T,>(
    pageKey: string,
    remoteKey?: string
  ): T | undefined => {
    const key = buildKey(pageKey, remoteKey);
    return storeRef.current[key] as T | undefined;
  }, []);

  const writeState = React.useCallback(
    <T,>(pageKey: string, remoteKey: string | undefined, value: T) => {
      const key = buildKey(pageKey, remoteKey);
      setStore((prev) => {
        const existing = prev[key];
        if (valuesAreEqual(existing, value)) {
          return prev;
        }

        const next = {
          ...prev,
          [key]: value,
        };
        storeRef.current = next;
        persistStore(next);
        return next;
      });
    },
    [persistStore]
  );

  const resetState = React.useCallback(
    (pageKey: string, remoteKey?: string) => {
      const key = buildKey(pageKey, remoteKey);
      setStore((prev) => {
        if (!(key in prev)) {
          return prev;
        }
        const next = { ...prev };
        delete next[key];
        storeRef.current = next;
        persistStore(next);
        return next;
      });
    },
    [persistStore]
  );

  const value = React.useMemo(
    () => ({
      readState,
      writeState,
      resetState,
    }),
    [readState, writeState, resetState]
  );

  return (
    <SearchStateContext.Provider value={value}>
      {children}
    </SearchStateContext.Provider>
  );
}

export function useSearchState() {
  const ctx = React.useContext(SearchStateContext);
  if (!ctx) {
    throw new Error(
      'useSearchState must be used within a SearchStateProvider'
    );
  }
  return ctx;
}
