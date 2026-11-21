// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useEffect, useRef, useState } from 'react';
import type { StoredDraft } from '../contracts';
import { readDraft, removeDraft, writeDraft } from './draftStorage';

const STORE_DELAY_MS = 1000;

interface DraftRecoveryInput {
  storageKey: string;
  serverText?: string;
  /** Hash of the server version that the edited text is based on. */
  serverSha256?: string;
  currentText?: string;
  isDirty: boolean;
  /**
   * Hash of the server version that the edits start from, stored with them.
   * It wins over the hash that the hook tracks from `serverSha256`.
   */
  baseSha256?: string;
  /** Tells if two texts are the same content; default strict equality. */
  isSame?(a: string, b: string): boolean;
}

const strictlyEqual = (a: string, b: string) => a === b;

// The stored draft is read at mount, before any write of this mount can
// replace it. The offer is decided once serverText is known.
interface RecoveryState {
  key: string;
  stored: StoredDraft | null;
  offered: StoredDraft | null;
  decided: boolean;
}

function initialState(key: string): RecoveryState {
  return { key, stored: readDraft(key), offered: null, decided: false };
}

interface DraftRecovery {
  draft: StoredDraft | null;
  discard(): void;
  /**
   * Drops the offer. The host puts the draft text back; from then on the text
   * is based on the version of the draft, not on the one of the server.
   */
  restore(): void;
}

interface PendingDraft {
  key: string;
  text: string;
  baseSha256?: string;
}

function writePending(next: PendingDraft): void {
  writeDraft(next.key, {
    text: next.text,
    savedAt: Date.now(),
    baseSha256: next.baseSha256,
  });
}

/**
 * Keeps unsaved editor text in browser storage and offers it back after a
 * reload. The stored draft carries the hash of the server version that it is
 * based on, so that the host can tell a draft of an older version. The hash is
 * `baseSha256` when the host gives it, else the one that the hook tracks from
 * `serverSha256`.
 */
export function useDraftRecovery({
  storageKey,
  serverText,
  serverSha256,
  currentText,
  isDirty,
  baseSha256,
  isSame = strictlyEqual,
}: DraftRecoveryInput): DraftRecovery {
  const [state, setState] = useState(() => initialState(storageKey));
  let current = state;
  if (current.key !== storageKey) {
    current = initialState(storageKey);
    setState(current);
  }
  if (!current.decided && serverText !== undefined) {
    const { stored } = current;
    current = {
      ...current,
      offered:
        stored !== null && !isSame(stored.text, serverText) ? stored : null,
      decided: true,
    };
    setState(current);
  }

  // The server version that the text was edited from: a clean text follows
  // the server, a dirty one keeps the version that it had when the edit began.
  const trackedBase = useRef(serverSha256);

  const discard = useCallback(() => {
    removeDraft(storageKey);
    setState((prev) => ({ ...prev, stored: null, offered: null }));
  }, [storageKey]);

  const offered = current.offered;
  const restore = useCallback(() => {
    trackedBase.current = offered?.baseSha256;
    discard();
  }, [offered, discard]);

  // The key for which this mount has seen dirty text. A clean mount must keep
  // the stored draft until the user can recover it.
  const dirtyKey = useRef<string | null>(null);
  useEffect(() => {
    if (isDirty) {
      dirtyKey.current = storageKey;
    } else if (
      dirtyKey.current === storageKey &&
      currentText !== undefined &&
      serverText !== undefined &&
      isSame(currentText, serverText)
    ) {
      removeDraft(storageKey);
      dirtyKey.current = null;
    }
  }, [storageKey, serverText, currentText, isDirty, isSame]);

  // Text not yet written, for the flush on page hide, on unmount and when
  // the editor switches to another key before the delay ends.
  const pending = useRef<PendingDraft | null>(null);

  useEffect(() => {
    if (pending.current !== null && pending.current.key !== storageKey) {
      writePending(pending.current);
    }
    if (!isDirty || currentText === undefined) {
      trackedBase.current = serverSha256;
      pending.current = null;
      return;
    }
    const base = baseSha256 ?? trackedBase.current;
    const next: PendingDraft = {
      key: storageKey,
      text: currentText,
      baseSha256: base,
    };
    pending.current = next;
    const timer = setTimeout(() => {
      pending.current = null;
      writePending(next);
    }, STORE_DELAY_MS);
    return () => clearTimeout(timer);
  }, [storageKey, currentText, serverSha256, isDirty, baseSha256]);

  useEffect(() => {
    const flush = () => {
      const next = pending.current;
      if (next === null) return;
      pending.current = null;
      writePending(next);
    };
    window.addEventListener('pagehide', flush);
    return () => {
      window.removeEventListener('pagehide', flush);
      flush();
    };
  }, []);

  return { draft: current.offered, discard, restore };
}
