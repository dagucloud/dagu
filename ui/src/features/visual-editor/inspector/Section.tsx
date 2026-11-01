// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ChevronDown, ChevronRight } from 'lucide-react';
import { useId, useState, type ReactElement, type ReactNode } from 'react';

export interface SectionProps {
  title: ReactNode;
  defaultOpen?: boolean;
  /** Number of issues in the section; a badge shows it when it is not 0. */
  count?: number;
  /** A new value opens the section, so that a field in it can take focus. */
  revealKey?: number;
  children: ReactNode;
}

/** Collapsible block of the inspector with a dense header button. */
export function Section({
  title,
  defaultOpen = true,
  count = 0,
  revealKey,
  children,
}: SectionProps): ReactElement {
  const [open, setOpen] = useState(defaultOpen);
  // Opens in the same render, so that the field exists when the caller's
  // effect focuses it.
  const [seenReveal, setSeenReveal] = useState(revealKey);
  if (revealKey !== seenReveal) {
    setSeenReveal(revealKey);
    if (revealKey !== undefined) {
      setOpen(true);
    }
  }
  const bodyId = useId();
  return (
    <section className="border-b border-border">
      <button
        type="button"
        className="flex h-7 w-full items-center gap-1 px-2 text-left text-xs font-semibold text-foreground hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
        aria-expanded={open}
        aria-controls={bodyId}
        onClick={() => setOpen((value) => !value)}
      >
        {open ? (
          <ChevronDown className="size-3.5 shrink-0" aria-hidden />
        ) : (
          <ChevronRight className="size-3.5 shrink-0" aria-hidden />
        )}
        <span className="flex-1 truncate">{title}</span>
        {count > 0 && (
          <span className="rounded bg-destructive/10 px-1 text-[10px] font-medium text-destructive">
            {count}
          </span>
        )}
      </button>
      <div id={bodyId} hidden={!open} className="space-y-2 px-2 pb-2 pt-1">
        {open ? children : null}
      </div>
    </section>
  );
}
