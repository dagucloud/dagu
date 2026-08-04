// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useEffect, useRef } from 'react';
import { useDocTabContext } from '@/contexts/DocTabContext';

export function useDocDraftPersistence({
  draftKey,
  currentValue,
  hasUnsavedChanges,
  setCurrentValue,
}: {
  draftKey: string;
  currentValue: string | null;
  hasUnsavedChanges: boolean;
  setCurrentValue: (value: string) => void;
}) {
  const { getDraft, setDraft, clearDraft } = useDocTabContext();
  const currentValueRef = useRef(currentValue);
  currentValueRef.current = currentValue;
  const hasUnsavedChangesRef = useRef(hasUnsavedChanges);
  hasUnsavedChangesRef.current = hasUnsavedChanges;
  const persistenceTimerRef = useRef<ReturnType<typeof setTimeout> | null>(
    null
  );

  useEffect(() => {
    const draft = getDraft(draftKey);
    if (draft !== undefined) setCurrentValue(draft);
  }, [draftKey, getDraft, setCurrentValue]);

  const persistDraft = useCallback(() => {
    if (hasUnsavedChangesRef.current) {
      setDraft(draftKey, currentValueRef.current ?? '');
    }
  }, [draftKey, setDraft]);

  useEffect(() => () => persistDraft(), [persistDraft]);

  useEffect(() => {
    if (!hasUnsavedChanges) return;

    const timer = setTimeout(() => {
      setDraft(draftKey, currentValue ?? '');
      if (persistenceTimerRef.current === timer) {
        persistenceTimerRef.current = null;
      }
    }, 300);
    persistenceTimerRef.current = timer;
    return () => {
      clearTimeout(timer);
      if (persistenceTimerRef.current === timer) {
        persistenceTimerRef.current = null;
      }
    };
  }, [currentValue, draftKey, hasUnsavedChanges, setDraft]);

  useEffect(() => {
    window.addEventListener('pagehide', persistDraft);
    return () => window.removeEventListener('pagehide', persistDraft);
  }, [persistDraft]);

  const clearPersistedDraft = useCallback(() => {
    if (persistenceTimerRef.current) {
      clearTimeout(persistenceTimerRef.current);
      persistenceTimerRef.current = null;
    }
    hasUnsavedChangesRef.current = false;
    clearDraft(draftKey);
  }, [clearDraft, draftKey]);

  return {
    clearPersistedDraft,
    currentValueRef,
    hasUnsavedChangesRef,
  };
}
