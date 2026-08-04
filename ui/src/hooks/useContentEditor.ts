import { useCallback, useEffect, useRef, useState } from 'react';

interface ConflictState {
  hasConflict: boolean;
  externalContent: string | null;
}

interface UseContentEditorOptions {
  /** Key for resetting state on navigation (e.g., fileName) */
  key: string;
  /** Server content from any source (SSE or polling) */
  serverContent: string | null;
}

interface UseContentEditorResult {
  /** Current editor value. null = not yet initialized. */
  currentValue: string | null;
  setCurrentValue: (value: string) => void;
  hasUnsavedChanges: boolean;
  conflict: ConflictState;
  resolveConflict: (action: 'discard' | 'ignore') => void;
  beginSave: (content: string) => void;
  cancelSave: () => void;
  markAsSaved: (savedContent: string) => void;
  /** Revert editor to the last known server content, discarding all local edits. */
  discardChanges: () => void;
}

/**
 * Generic content editor hook with conflict detection.
 * Decoupled from data transport — receives serverContent from any source.
 * Detects when content changes externally while the user is editing.
 */
export function useContentEditor({
  key,
  serverContent,
}: UseContentEditorOptions): UseContentEditorResult {
  // Track local edits (null = not yet initialized)
  const [currentValue, setCurrentValueState] = useState<string | null>(null);

  // Track the last known server content for rendering and change detection.
  const [lastServerContent, setLastServerContentState] = useState<
    string | null
  >(null);
  const lastServerContentRef = useRef<string | null>(null);
  const setLastServerContent = useCallback((content: string | null) => {
    lastServerContentRef.current = content;
    setLastServerContentState(content);
  }, []);

  // Track if user has started editing
  const hasUserEditedRef = useRef<boolean>(false);

  // Track pending save content so its server echo is not treated as external.
  const pendingSaveContentRef = useRef<string | null>(null);

  // Ref for currentValue to avoid effect re-runs on every keystroke
  const currentValueRef = useRef<string | null>(null);

  // Conflict state
  const [conflict, setConflict] = useState<ConflictState>({
    hasConflict: false,
    externalContent: null,
  });

  // Reset all state when key changes (navigating to different item)
  useEffect(() => {
    setLastServerContent(null);
    hasUserEditedRef.current = false;
    pendingSaveContentRef.current = null;
    currentValueRef.current = null;
    setCurrentValueState(null);
    setConflict({ hasConflict: false, externalContent: null });
  }, [key, setLastServerContent]);

  // Process incoming server content changes
  useEffect(() => {
    if (serverContent == null) {
      return;
    }

    // First load - initialize everything
    if (lastServerContentRef.current === null) {
      setLastServerContent(serverContent);
      if (!hasUserEditedRef.current) {
        currentValueRef.current = serverContent;
        setCurrentValueState(serverContent);
      }
      return;
    }

    // Check if this is the pending save coming back.
    if (pendingSaveContentRef.current === serverContent) {
      setLastServerContent(serverContent);
      pendingSaveContentRef.current = null;
      return;
    }

    // Check if server content actually changed
    if (serverContent === lastServerContentRef.current) {
      return;
    }

    // Server content changed externally
    const hasLocalChanges =
      hasUserEditedRef.current &&
      currentValueRef.current !== lastServerContentRef.current;

    if (hasLocalChanges) {
      // Conflict: user has unsaved edits AND external change occurred
      setConflict({
        hasConflict: true,
        externalContent: serverContent,
      });
    } else {
      // No local edits - update silently
      setLastServerContent(serverContent);
      currentValueRef.current = serverContent;
      setCurrentValueState(serverContent);
      hasUserEditedRef.current = false;
    }
  }, [key, serverContent, setLastServerContent]);

  // Handle user edits
  const setCurrentValue = useCallback((value: string) => {
    hasUserEditedRef.current = true;
    currentValueRef.current = value;
    setCurrentValueState(value);
  }, []);

  // Resolve conflict
  const resolveConflict = useCallback(
    (action: 'discard' | 'ignore') => {
      if (action === 'discard') {
        // Discard local changes, accept external
        if (conflict.externalContent !== null) {
          setLastServerContent(conflict.externalContent);
          currentValueRef.current = conflict.externalContent;
          setCurrentValueState(conflict.externalContent);
          hasUserEditedRef.current = false;
        }
      } else {
        // Ignore external changes, keep local
        // Just update the server ref to prevent repeated dialogs
        if (conflict.externalContent !== null) {
          setLastServerContent(conflict.externalContent);
        }
      }
      setConflict({ hasConflict: false, externalContent: null });
    },
    [conflict.externalContent, setLastServerContent]
  );

  // Discard local edits and revert to last known server content
  const discardChanges = useCallback(() => {
    if (lastServerContentRef.current !== null) {
      currentValueRef.current = lastServerContentRef.current;
      setCurrentValueState(lastServerContentRef.current);
      hasUserEditedRef.current = false;
    }
  }, []);

  const beginSave = useCallback((content: string) => {
    pendingSaveContentRef.current = content;
  }, []);

  const cancelSave = useCallback(() => {
    pendingSaveContentRef.current = null;
  }, []);

  // Called after successful save
  const markAsSaved = useCallback(
    (savedContent: string) => {
      pendingSaveContentRef.current = savedContent;
      setLastServerContent(savedContent);
      hasUserEditedRef.current = currentValueRef.current !== savedContent;
    },
    [setLastServerContent]
  );

  // Calculate unsaved changes
  const hasUnsavedChanges =
    lastServerContent !== null &&
    currentValue !== null &&
    currentValue !== lastServerContent;

  return {
    currentValue,
    setCurrentValue,
    hasUnsavedChanges,
    conflict,
    resolveConflict,
    beginSave,
    cancelSave,
    markAsSaved,
    discardChanges,
  };
}
