import React, {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useRef,
  useState,
} from 'react';

/** Saves the edits. Resolves true only when the server has them. */
type SaveHandler = () => Promise<boolean>;

interface UnsavedChangesContextType {
  hasUnsavedChanges: boolean;
  setHasUnsavedChanges: (value: boolean) => void;
  /** True while a mounted editor offers to save its edits. */
  canSave: boolean;
  /**
   * Saves the edits of the editor. Resolves false when nothing was saved; the
   * editor then shows the reason.
   */
  save: SaveHandler;
  /**
   * Offers the save of an editor to the readers of the context. Returns the
   * function that withdraws the offer.
   */
  registerSave: (handler: SaveHandler) => () => void;
}

const UnsavedChangesContext = createContext<UnsavedChangesContextType | null>(
  null
);

const outsideProvider: UnsavedChangesContextType = {
  hasUnsavedChanges: false,
  setHasUnsavedChanges: () => {},
  canSave: false,
  save: async () => false,
  registerSave: () => () => {},
};

export function useUnsavedChanges() {
  return useContext(UnsavedChangesContext) ?? outsideProvider;
}

export function UnsavedChangesProvider({
  children,
}: {
  children: React.ReactNode;
}) {
  const [hasUnsavedChanges, setHasUnsavedChangesState] = useState(false);
  const [canSave, setCanSave] = useState(false);
  const saveHandler = useRef<SaveHandler | null>(null);

  const setHasUnsavedChanges = useCallback((value: boolean) => {
    setHasUnsavedChangesState(value);
  }, []);

  const save = useCallback<SaveHandler>(async () => {
    try {
      return (await saveHandler.current?.()) ?? false;
    } catch (error) {
      // A caller starts a run on true: a save that threw saved nothing.
      console.error('Failed to save the edits:', error);
      return false;
    }
  }, []);

  const registerSave = useCallback((handler: SaveHandler) => {
    saveHandler.current = handler;
    setCanSave(true);
    return () => {
      // A later editor may have taken the place of this one.
      if (saveHandler.current !== handler) return;
      saveHandler.current = null;
      setCanSave(false);
    };
  }, []);

  // One value while the flags are the same: a render of the parent does not
  // render every reader.
  const value = useMemo(
    () => ({
      hasUnsavedChanges,
      setHasUnsavedChanges,
      canSave,
      save,
      registerSave,
    }),
    [hasUnsavedChanges, setHasUnsavedChanges, canSave, save, registerSave]
  );

  return (
    <UnsavedChangesContext.Provider value={value}>
      {children}
    </UnsavedChangesContext.Provider>
  );
}
