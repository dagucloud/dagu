// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useState, type ReactElement } from 'react';
import { Input } from '@/components/ui/input';
import { useI18n } from '@/i18n/I18nProvider';
import type { ReferenceCandidate } from '../contracts';
import {
  filterReferenceCandidates,
  REFERENCE_GROUP_LABELS,
  REFERENCE_GROUP_ORDER,
} from './candidates';
import { ReferenceName, type InsertToken } from './ReferenceName';

export interface AvailableReferencesProps {
  candidates: ReferenceCandidate[];
  onInsert?: InsertToken;
}

/**
 * Every reference that a field can read, by group, for an author who does not
 * know the names yet. A row inserts its token, or copies it when no field takes
 * it. Nothing shows without candidates.
 */
export function AvailableReferences({
  candidates,
  onInsert,
}: AvailableReferencesProps): ReactElement | null {
  const { ts } = useI18n();
  const [query, setQuery] = useState('');
  if (candidates.length === 0) return null;
  const shown = filterReferenceCandidates(candidates, query);
  const groups = REFERENCE_GROUP_ORDER.flatMap((group) => {
    const items = shown.filter((candidate) => candidate.group === group);
    return items.length > 0 ? [{ group, items }] : [];
  });
  return (
    <section className="flex flex-col gap-1 border-t border-border pt-2">
      <h4 className="text-xs font-semibold text-muted-foreground">
        {ts('Available references')}
      </h4>
      <Input
        type="search"
        className="h-7 px-2 text-xs"
        value={query}
        placeholder={ts('Search references')}
        aria-label={ts('Search references')}
        onChange={(event) => setQuery(event.target.value)}
      />
      {groups.length === 0 && (
        <p role="status" className="text-xs text-muted-foreground">
          {ts('No matching references')}
        </p>
      )}
      {groups.map(({ group, items }) => {
        // A group that only a run gives says so once, not on each row.
        const allRuntime =
          items.every((c) => c.known === 'runtime') &&
          !items.some((c) => c.publish);
        return (
          <div
            key={group}
            role="group"
            aria-label={ts(REFERENCE_GROUP_LABELS[group])}
            className="flex flex-col gap-0.5"
          >
            <div className="flex items-center justify-between gap-2">
              <span className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
                {ts(REFERENCE_GROUP_LABELS[group])}
              </span>
              {allRuntime && <Badge text="Known after run" />}
            </div>
            {items.map((candidate) => (
              <div
                key={`${candidate.stepName ?? ''}:${candidate.token}`}
                className="flex min-w-0 items-baseline gap-2"
              >
                <ReferenceName
                  name={candidate.label}
                  reference={candidate.token}
                  onInsert={(token) => {
                    candidate.onPicked?.();
                    return onInsert?.(token);
                  }}
                />
                {candidate.detail && (
                  <span className="min-w-0 truncate text-muted-foreground">
                    {candidate.detail}
                  </span>
                )}
                {candidate.publish ? (
                  <Badge text="Published when chosen" />
                ) : (
                  candidate.known === 'runtime' &&
                  !allRuntime && <Badge text="Known after run" />
                )}
              </div>
            ))}
          </div>
        );
      })}
    </section>
  );
}

function Badge({ text }: { text: string }): ReactElement {
  const { ts } = useI18n();
  return (
    <span className="ml-auto shrink-0 rounded-sm bg-warning/10 px-1 text-[10px] text-warning">
      {ts(text)}
    </span>
  );
}
