// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactElement } from 'react';
import { Check } from 'lucide-react';
import { useCopyFeedback } from '@/hooks/useCopyFeedback';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';

/** Puts a token in the field that has the focus. False when no field takes it. */
export type InsertToken = (token: string) => boolean | void;

/**
 * A name that inserts its reference at the cursor of the field that has the
 * focus, or copies it when no field takes it. A copy shows a Check on the name
 * for 2 s.
 */
export function ReferenceName({
  name,
  reference,
  onInsert,
}: {
  name: string;
  reference: string | null;
  onInsert?: InsertToken;
}): ReactElement {
  const { ts } = useI18n();
  const { copied, copy } = useCopyFeedback(2000);
  if (!reference) return <span className="font-mono font-medium">{name}</span>;
  return (
    <button
      type="button"
      title={reference}
      aria-label={copied ? ts('Copied') : undefined}
      // A text field takes the plain text of a drop at the point of the drop.
      draggable
      onDragStart={(event) => {
        event.dataTransfer.setData('text/plain', reference);
        event.dataTransfer.effectAllowed = 'copy';
      }}
      onClick={() => {
        if (onInsert && onInsert(reference) !== false) return;
        void copy(reference);
      }}
      className="relative rounded font-mono font-medium hover:underline focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
    >
      {/* The name keeps its width, so the row does not move. */}
      <span className={cn(copied && 'invisible')}>{name}</span>
      {copied && (
        <Check
          className="absolute inset-0 m-auto size-3 text-success"
          aria-hidden="true"
        />
      )}
    </button>
  );
}
