// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * The card over the canvas that offers the draft that an earlier visit left.
 *
 * @module features/dags/components/dag-editor
 */
import { ArchiveRestore, Trash2 } from 'lucide-react';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import { NoticeLine } from './NoticeLine';

export interface DraftRecoveryCardProps {
  /** When the draft was stored, in milliseconds. */
  savedAt: number;
  onRestore(): void;
  onDiscard(): void;
}

/**
 * A compact card for the top centre of the canvas. It does not take room from
 * the canvas: the host places it over the canvas, in an absolute layer.
 */
export function DraftRecoveryCard({
  savedAt,
  onRestore,
  onDiscard,
}: DraftRecoveryCardProps) {
  const { ts } = useI18n();
  return (
    <div
      data-testid="draft-recovery"
      className="pointer-events-auto max-w-full rounded-md bg-card shadow-md"
    >
      <NoticeLine
        notices={[
          {
            id: 'draft',
            tone: 'warning',
            priority: 0,
            label: ts('Unsaved draft'),
            message: ts('Restore unsaved draft from {time}?', {
              time: formatAbsoluteTime(savedAt) ?? '',
            }),
            action: {
              label: (
                <>
                  <ArchiveRestore className="h-3 w-3" aria-hidden="true" />
                  {ts('Restore')}
                </>
              ),
              run: onRestore,
            },
            secondaryAction: {
              label: (
                <>
                  <Trash2 className="h-3 w-3" aria-hidden="true" />
                  {ts('Discard')}
                </>
              ),
              run: onDiscard,
            },
          },
        ]}
      />
    </div>
  );
}
