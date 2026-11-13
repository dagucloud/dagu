// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  AlertTriangle,
  CheckCircle2,
  Loader2,
  type LucideIcon,
  XCircle,
} from 'lucide-react';
import { BackfillStatus } from '@/api/v1/schema';
import { useI18n } from '@/i18n/I18nProvider';
import {
  type Backfill,
  summarizeBackfill,
  type SummaryTone,
} from '../lib/backfillState';

const TONE: Record<SummaryTone, { icon: LucideIcon; color: string }> = {
  running: { icon: Loader2, color: 'var(--status-running)' },
  success: { icon: CheckCircle2, color: 'var(--status-success)' },
  error: { icon: XCircle, color: 'var(--status-error)' },
  warning: { icon: AlertTriangle, color: 'var(--status-warning)' },
};

/**
 * Where a backfill stands in one sentence, and why an open one makes no
 * progress when something holds it.
 */
export function BackfillSentence({ backfill }: { backfill: Backfill }) {
  const { ts } = useI18n();
  const summary = summarizeBackfill(backfill);
  const tone = TONE[summary.tone];
  const Icon = tone.icon;
  const why =
    backfill.status === BackfillStatus.waiting_for_scheduler
      ? ts('No scheduler is running, so the backfill queues no run.')
      : backfill.waiting;
  return (
    <div className="flex flex-col gap-0.5">
      <p className="flex items-center gap-2 text-sm font-medium">
        <Icon
          className="h-4 w-4 shrink-0"
          style={{ color: tone.color }}
          aria-hidden="true"
        />
        {ts(summary.message, summary.values)}
      </p>
      {why && <p className="pl-6 text-xs text-muted-foreground">{why}</p>}
    </div>
  );
}
