// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
  type ReactElement,
  type Ref,
} from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import type { ReferenceCandidate } from '../contracts';
import {
  filterReferenceCandidates,
  REFERENCE_GROUP_LABELS,
  REFERENCE_GROUP_ORDER,
} from './candidates';

export interface ReferencePickerHandle {
  /** Handle ArrowDown, ArrowUp, Enter or Escape. Returns true when the picker used the key. */
  handleKey(key: string): boolean;
}

export interface ReferencePickerProps {
  /** Id of the listbox element. Option ids derive from it. */
  id: string;
  candidates: ReferenceCandidate[];
  /** Show a search box and focus it. The box then sets the filter and `query` is ignored. */
  showSearch?: boolean;
  /** Filter text from the host, for use without a search box. */
  query?: string;
  onPick(candidate: ReferenceCandidate): void;
  onClose(): void;
  /** Id of the active option, or undefined when the list is empty. */
  onActiveChange?(optionId: string | undefined): void;
  ref?: Ref<ReferencePickerHandle>;
}

/** Candidates in display order: grouped by REFERENCE_GROUP_ORDER, input order inside a group. */
function orderCandidates(
  candidates: ReferenceCandidate[]
): ReferenceCandidate[] {
  return REFERENCE_GROUP_ORDER.flatMap((group) =>
    candidates.filter((c) => c.group === group)
  );
}

/**
 * Panel that lists reference candidates grouped by source.
 * The parent places it: the panel is absolute, below its relative wrapper.
 */
export function ReferencePicker({
  id,
  candidates,
  showSearch,
  query,
  onPick,
  onClose,
  onActiveChange,
  ref,
}: ReferencePickerProps): ReactElement {
  const { ts } = useI18n();
  const listRef = useRef<HTMLDivElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const [activeIndex, setActiveIndex] = useState(0);
  const [search, setSearch] = useState('');

  const filter = showSearch ? search : (query ?? '');
  const items = useMemo(
    () => orderCandidates(filterReferenceCandidates(candidates, filter)),
    [candidates, filter]
  );
  const active =
    items.length === 0 ? -1 : Math.min(activeIndex, items.length - 1);
  const optionId = (index: number) => `${id}-opt-${index}`;
  const activeId = active >= 0 ? optionId(active) : undefined;

  // Start at the first option each time the filter changes the list.
  const listKey = items.map((c) => c.token).join('\n');
  useEffect(() => {
    setActiveIndex(0);
  }, [listKey]);

  useEffect(() => {
    if (showSearch) searchRef.current?.focus();
  }, [showSearch]);

  useEffect(() => {
    onActiveChange?.(activeId);
  }, [activeId, onActiveChange]);

  useEffect(() => {
    if (active < 0) return;
    listRef.current
      ?.querySelector<HTMLElement>(`[data-index="${active}"]`)
      ?.scrollIntoView?.({ block: 'nearest' });
  }, [active]);

  const handleKey = (key: string): boolean => {
    const count = items.length;
    switch (key) {
      case 'ArrowDown':
        if (count > 0) setActiveIndex((active + 1) % count);
        return count > 0;
      case 'ArrowUp':
        if (count > 0) setActiveIndex((active - 1 + count) % count);
        return count > 0;
      case 'Enter':
        if (!items[active]) return false;
        onPick(items[active]);
        return true;
      case 'Escape':
        onClose();
        return true;
      default:
        return false;
    }
  };
  useImperativeHandle(ref, () => ({ handleKey }));

  return (
    <div
      className="absolute left-0 top-full z-50 mt-1 w-full min-w-64 overflow-hidden rounded-md border border-border bg-popover text-popover-foreground shadow-md"
      // Keep the focus where it is: a click inside must not blur the input.
      onMouseDown={(e) => {
        if (e.target !== searchRef.current) e.preventDefault();
      }}
    >
      {showSearch ? (
        <input
          ref={searchRef}
          type="text"
          value={search}
          placeholder={ts('Search references')}
          aria-label={ts('Search references')}
          aria-controls={id}
          aria-activedescendant={activeId}
          onChange={(e) => setSearch(e.target.value)}
          onKeyDown={(e) => {
            if (handleKey(e.key)) {
              e.preventDefault();
              e.stopPropagation();
            }
          }}
          className="block h-7 w-full border-b border-border bg-transparent px-2 text-xs text-foreground placeholder:text-muted-foreground focus-visible:outline-none"
        />
      ) : null}
      <div
        ref={listRef}
        id={id}
        role="listbox"
        aria-label={ts('References')}
        className={cn(
          'max-h-60 overflow-y-auto py-1',
          items.length === 0 && 'hidden'
        )}
      >
        {REFERENCE_GROUP_ORDER.map((group) => {
          const first = items.findIndex((c) => c.group === group);
          if (first < 0) return null;
          const headerId = `${id}-group-${group}`;
          return (
            <div key={group} role="group" aria-labelledby={headerId}>
              <div
                id={headerId}
                className="px-2 pb-0.5 pt-1.5 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground"
              >
                {ts(REFERENCE_GROUP_LABELS[group])}
              </div>
              {items.map((c, index) =>
                c.group !== group ? null : (
                  <div
                    key={`${c.token}-${index}`}
                    id={optionId(index)}
                    data-index={index}
                    role="option"
                    aria-selected={index === active}
                    onClick={() => onPick(c)}
                    onMouseMove={() => setActiveIndex(index)}
                    className={cn(
                      'cursor-pointer px-2 py-1 text-xs',
                      index === active && 'bg-accent text-accent-foreground'
                    )}
                  >
                    <div className="flex items-center gap-2">
                      <span className="truncate font-medium">{c.label}</span>
                      {c.detail ? (
                        <span className="truncate text-muted-foreground">
                          {c.detail}
                        </span>
                      ) : null}
                      {c.known === 'runtime' ? (
                        <span className="ml-auto shrink-0 rounded-sm bg-warning/10 px-1 text-[10px] text-warning">
                          {ts('Known after run')}
                        </span>
                      ) : null}
                    </div>
                    <div className="truncate font-mono text-[10px] text-muted-foreground">
                      {c.token}
                    </div>
                  </div>
                )
              )}
            </div>
          );
        })}
      </div>
      {items.length === 0 ? (
        <div role="status" className="px-2 py-2 text-xs text-muted-foreground">
          {candidates.length === 0
            ? ts('No references available')
            : ts('No matching references')}
        </div>
      ) : null}
    </div>
  );
}
