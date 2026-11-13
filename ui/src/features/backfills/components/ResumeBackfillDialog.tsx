// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { AlertTriangle } from 'lucide-react';
import { useEffect, useState } from 'react';
import {
  BackfillOnFailure,
  BackfillPlanSlotAction,
  type components,
} from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { useClient } from '@/hooks/api';
import { useI18n } from '@/i18n/I18nProvider';
import { formatRoughDurationMs } from '@/lib/formatDuration';
import type { Backfill } from '../lib/backfillState';

type BackfillPlan = components['schemas']['BackfillPlan'];
type SubmitResponse = components['schemas']['BackfillSubmitResponse'];

type Props = {
  backfill: Backfill;
  remoteNode: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onResumed: (backfill: Backfill) => void;
};

/** How long the options rest before the plan is asked for again. */
const REPLAN_DELAY_MS = 400;

/** The message of an API error, which a stale plan's answer lacks. */
function messageOf(error: unknown, fallback: string): string {
  return error !== null &&
    typeof error === 'object' &&
    'message' in error &&
    typeof error.message === 'string'
    ? error.message
    : fallback;
}

/**
 * Shows what resuming a backfill would run, with its concurrency and failure
 * behavior, and resumes it with the plan shown. A plan that changed in the
 * meantime is shown instead of resuming.
 */
export function ResumeBackfillDialog({
  backfill,
  remoteNode,
  open,
  onOpenChange,
  onResumed,
}: Props) {
  const { ts } = useI18n();
  const client = useClient();
  const [concurrency, setConcurrency] = useState(backfill.maxConcurrency);
  const [onFailure, setOnFailure] = useState(backfill.onFailure);
  const [plan, setPlan] = useState<BackfillPlan | null>(null);
  const [stale, setStale] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const validConcurrency = Number.isInteger(concurrency) && concurrency >= 1;

  useEffect(() => {
    if (!open || !validConcurrency) return;
    let current = true;
    const timer = setTimeout(async () => {
      try {
        const { data, error: apiError } = await client.POST(
          '/backfills/{backfillId}/resume',
          {
            params: {
              path: { backfillId: backfill.id },
              query: { remoteNode },
            },
            body: { maxConcurrency: concurrency, onFailure, dryRun: true },
          }
        );
        if (!current) return;
        if (data) {
          setPlan(data.plan);
          setStale(false);
          setError(null);
        } else {
          setError(messageOf(apiError, ts('Failed to resume the backfill')));
        }
      } catch (err) {
        if (current) setError(err instanceof Error ? err.message : String(err));
      }
    }, REPLAN_DELAY_MS);
    return () => {
      current = false;
      clearTimeout(timer);
    };
  }, [
    open,
    validConcurrency,
    concurrency,
    onFailure,
    backfill.id,
    remoteNode,
    client,
    ts,
  ]);

  const changeOpen = (next: boolean) => {
    if (!next) {
      setPlan(null);
      setStale(false);
      setError(null);
      setConcurrency(backfill.maxConcurrency);
      setOnFailure(backfill.onFailure);
    }
    onOpenChange(next);
  };

  const runs =
    plan?.slots.filter((slot) => slot.action === BackfillPlanSlotAction.run)
      .length ?? 0;

  const resume = async () => {
    if (!plan) return;
    setSubmitting(true);
    setError(null);
    try {
      const {
        data,
        error: apiError,
        response,
      } = await client.POST('/backfills/{backfillId}/resume', {
        params: { path: { backfillId: backfill.id }, query: { remoteNode } },
        body: {
          maxConcurrency: concurrency,
          onFailure,
          revision: plan.revision,
        },
      });
      if (data?.backfill) {
        onResumed(data.backfill);
        changeOpen(false);
      } else if (response.status === 409 && apiError && 'plan' in apiError) {
        setPlan((apiError as SubmitResponse).plan);
        setStale(true);
      } else {
        setError(messageOf(apiError, ts('Failed to resume the backfill')));
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      <DialogContent className="sm:max-w-[520px]">
        <DialogHeader>
          <DialogTitle>{ts('Resume backfill')}</DialogTitle>
          <DialogDescription>
            {ts(
              'Runs again the slots that did not succeed, with the current definition of the DAG and new runs.'
            )}
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4 text-sm">
          <label className="flex flex-col gap-1">
            <span className="font-medium">{ts('Runs at once')}</span>
            <Input
              type="number"
              min={1}
              className="w-28"
              value={Number.isNaN(concurrency) ? '' : concurrency}
              onChange={(event) =>
                setConcurrency(Number.parseInt(event.target.value, 10))
              }
            />
            {plan && (
              <span className="text-xs text-muted-foreground">
                {ts('The queue runs {limit} at a time.', {
                  limit: plan.queueLimit,
                })}
              </span>
            )}
          </label>

          <fieldset className="flex flex-col gap-1.5">
            <legend className="mb-1 font-medium">
              {ts('After a slot fails')}
            </legend>
            {[
              {
                value: BackfillOnFailure.stop,
                label: 'Stop: for loads that build on the slot before',
              },
              {
                value: BackfillOnFailure.continue,
                label: 'Continue: for slots that do not depend on each other',
              },
            ].map((option) => (
              <label
                key={option.value}
                className="flex cursor-pointer items-center gap-2"
              >
                <input
                  type="radio"
                  name="backfill-on-failure"
                  checked={onFailure === option.value}
                  onChange={() => setOnFailure(option.value)}
                />
                {ts(option.label)}
              </label>
            ))}
          </fieldset>

          <div className="rounded-md border bg-muted/30 px-3 py-2">
            {!plan ? (
              <span className="text-muted-foreground">{ts('Planning…')}</span>
            ) : runs === 0 ? (
              <span>
                {ts('Nothing to resume: every slot succeeded or was skipped.')}
              </span>
            ) : (
              <div className="flex flex-col gap-1">
                <span className="font-medium">
                  {runs === 1
                    ? ts('Runs 1 slot')
                    : ts('Runs {count} slots', { count: runs })}
                  {plan.estimateSeconds ? (
                    <span className="ml-2 font-normal text-muted-foreground">
                      {ts('About {duration}', {
                        duration: formatRoughDurationMs(
                          plan.estimateSeconds * 1000
                        ),
                      })}
                    </span>
                  ) : null}
                </span>
                {(plan.warnings ?? []).map((warning) => (
                  <span
                    key={warning}
                    className="flex items-start gap-1.5 text-xs text-muted-foreground"
                  >
                    <AlertTriangle
                      className="mt-0.5 h-3.5 w-3.5 shrink-0"
                      style={{ color: 'var(--status-warning)' }}
                      aria-hidden="true"
                    />
                    {warning}
                  </span>
                ))}
              </div>
            )}
          </div>

          {stale && (
            <p
              role="status"
              className="text-sm"
              style={{ color: 'var(--status-warning)' }}
            >
              {ts('The plan changed. Review it and resume again.')}
            </p>
          )}
          {error && (
            <p role="alert" className="text-sm text-destructive">
              {error}
            </p>
          )}
        </div>

        <DialogFooter>
          <Button variant="ghost" size="sm" onClick={() => changeOpen(false)}>
            {ts('Cancel')}
          </Button>
          <Button
            variant="primary"
            size="sm"
            disabled={!plan || runs === 0 || !validConcurrency || submitting}
            onClick={() => void resume()}
          >
            {runs === 1
              ? ts('Resume 1 run')
              : ts('Resume {count} runs', { count: runs })}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
