// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useState } from 'react';
import { BackfillSlotState } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { useClient } from '@/hooks/api';
import { useI18n } from '@/i18n/I18nProvider';
import { type Backfill, slotsIn } from '../lib/backfillState';

type Props = {
  backfill: Backfill;
  remoteNode: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCancelled: (backfill: Backfill) => void;
};

/**
 * Asks before cancelling a backfill, and whether its runs in progress stop
 * too. A failure stays in the dialog, so the choice can be made again.
 */
export function CancelBackfillDialog({
  backfill,
  remoteNode,
  open,
  onOpenChange,
  onCancelled,
}: Props) {
  const { ts } = useI18n();
  const client = useClient();
  const [stopRunning, setStopRunning] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const inProgress =
    slotsIn(backfill, BackfillSlotState.running) +
    slotsIn(backfill, BackfillSlotState.waiting);

  const changeOpen = (next: boolean) => {
    if (!next) {
      setStopRunning(false);
      setError(null);
    }
    onOpenChange(next);
  };

  const cancel = async () => {
    setSubmitting(true);
    setError(null);
    try {
      const { data, error: apiError } = await client.POST(
        '/backfills/{backfillId}/cancel',
        {
          params: { path: { backfillId: backfill.id }, query: { remoteNode } },
          body: { stopRunning: inProgress > 0 && stopRunning },
        }
      );
      if (data) {
        onCancelled(data);
        changeOpen(false);
      } else {
        setError(apiError?.message ?? ts('Failed to cancel the backfill'));
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      <DialogContent className="sm:max-w-[460px]">
        <DialogHeader>
          <DialogTitle>{ts('Cancel the backfill?')}</DialogTitle>
          <DialogDescription>
            {ts(
              'Its queued runs are withdrawn and the slots it has not reached are cancelled. Runs in progress finish unless you stop them.'
            )}
          </DialogDescription>
        </DialogHeader>
        {inProgress > 0 && (
          <label className="flex cursor-pointer items-center gap-2 text-sm">
            <Checkbox
              checked={stopRunning}
              onCheckedChange={(checked) => setStopRunning(checked === true)}
            />
            {inProgress === 1
              ? ts('Also stop the run in progress')
              : ts('Also stop the {count} runs in progress', {
                  count: inProgress,
                })}
          </label>
        )}
        {error && (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        )}
        <DialogFooter>
          <Button variant="ghost" size="sm" onClick={() => changeOpen(false)}>
            {ts('Keep running')}
          </Button>
          <Button
            variant="destructive"
            size="sm"
            disabled={submitting}
            onClick={() => void cancel()}
          >
            {ts('Cancel backfill')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
