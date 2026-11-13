// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useRef, useState } from 'react';

/**
 * The draft of a text field that writes to the YAML on blur. It follows
 * `value` when the YAML changes under it (an undo, a change in the YAML pane),
 * and it sends the draft on blur, and when the field goes away, only when the
 * text differs from `value`. `onCommit` receives undefined for an empty text,
 * so that the key goes. A field that is read-only sends nothing.
 */
export function useCommittedDraft({
  value,
  readOnly,
  onCommit,
}: {
  value: string;
  readOnly: boolean;
  onCommit(text: string | undefined): void;
}): { draft: string; setDraft(text: string): void; commit(): void } {
  const [draft, setDraftState] = useState(value);
  const shown = useRef(value);
  const draftRef = useRef(value);
  const valueRef = useRef(value);
  valueRef.current = value;
  const commitRef = useRef(onCommit);
  commitRef.current = onCommit;
  const lockedRef = useRef(readOnly);
  lockedRef.current = readOnly;

  useEffect(() => {
    if (value !== shown.current) {
      shown.current = value;
      draftRef.current = value;
      setDraftState(value);
    }
  }, [value]);

  const commit = () => {
    if (lockedRef.current || draftRef.current === valueRef.current) return;
    commitRef.current(draftRef.current === '' ? undefined : draftRef.current);
  };
  // A step that is selected away from, with text not yet sent.
  const commitNow = useRef(commit);
  commitNow.current = commit;
  useEffect(() => () => commitNow.current(), []);

  return {
    draft,
    setDraft: (text) => {
      draftRef.current = text;
      setDraftState(text);
    },
    commit,
  };
}
