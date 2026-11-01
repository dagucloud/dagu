import React, { createContext, useContext, useState, useCallback, useMemo } from 'react';

interface UnsavedChangesContextType {
  hasUnsavedChanges: boolean;
  setHasUnsavedChanges: (value: boolean) => void;
}

const UnsavedChangesContext = createContext<UnsavedChangesContextType | null>(null);

export function useUnsavedChanges() {
  const context = useContext(UnsavedChangesContext);
  if (!context) {
    // Return a default object when used outside provider
    return { hasUnsavedChanges: false, setHasUnsavedChanges: () => {} };
  }
  return context;
}

export function UnsavedChangesProvider({ children }: { children: React.ReactNode }) {
  const [hasUnsavedChanges, setHasUnsavedChangesState] = useState(false);

  const setHasUnsavedChanges = useCallback((value: boolean) => {
    setHasUnsavedChangesState(value);
  }, []);

  // One value while the flag is the same: a render of the parent does not
  // render every reader.
  const value = useMemo(
    () => ({ hasUnsavedChanges, setHasUnsavedChanges }),
    [hasUnsavedChanges, setHasUnsavedChanges]
  );

  return (
    <UnsavedChangesContext.Provider value={value}>
      {children}
    </UnsavedChangesContext.Provider>
  );
}
