// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { AlertTriangle } from 'lucide-react';
import { useEffect, useState } from 'react';
import {
  BackfillOnFailure,
  BackfillPlanSlotAction,
  BackfillRerun,
  type components,
} from '@/api/v1/schema';
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
import { Input } from '@/components/ui/input';
import { useClient } from '@/hooks/api';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import { formatRoughDurationMs } from '@/lib/formatDuration';
import type { Backfill } from '../lib/backfillState';
import { RERUN_LABEL } from '../lib/backfillState';

type BackfillPlan = components['schemas']['BackfillPlan'];
type BackfillRequest = Omit<components['schemas']['BackfillRequest'], 'dryRun'>;
type SubmitResponse = components['schemas']['BackfillSubmitResponse'];

/** What a backfill runs: the slots of a range, or slots picked one by one. */
export type BackfillTarget =
  | { kind: 'range'; from: string; to?: string }
  | { kind: 'slots'; slots: readonly string[] };

type Props = {
  fileName: string;
  remoteNode: string;
  target: BackfillTarget;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** The backfill that started, or the open one that has the same options. */
  onStarted: (backfill: Backfill, existing: boolean) => void;
};

/** How long the options rest before the plan is asked for again. */
const REPLAN_DELAY_MS = 400;

const RERUNS: readonly BackfillRerun[] = [
  BackfillRerun.none,
  BackfillRerun.failed,
  BackfillRerun.all,
];

/** The options of a backfill that the dialog sets. */
interface StartOptions {
  rerun: BackfillRerun;
  concurrency: number;
  onFailure: BackfillOnFailure;
  params: string;
}

/** The request that plans or starts a backfill of target with options. */
function requestBody(
  target: BackfillTarget,
  { rerun, concurrency, onFailure, params }: StartOptions
): BackfillRequest {
  return {
    ...(target.kind === 'slots'
      ? { slots: [...target.slots] }
      : { from: target.from, ...(target.to ? { to: target.to } : {}), rerun }),
    maxConcurrency: concurrency,
    onFailure,
    ...(params.trim() ? { params: params.trim() } : {}),
  };
}

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
 * Plans a backfill of a DAG and starts it with the plan shown. For a range,
 * it offers what to run with the count each choice runs; slots picked one by
 * one run as they are. Redoing slots that succeeded needs a confirmation
 * that names how many. A plan that changed in the meantime is shown instead
 * of starting.
 */
export function StartBackfillDialog({
  fileName,
  remoteNode,
  target,
  open,
  onOpenChange,
  onStarted,
}: Props) {
  const { ts } = useI18n();
  const client = useClient();
  const [rerun, setRerun] = useState(BackfillRerun.none);
  const [concurrency, setConcurrency] = useState(1);
  const [onFailure, setOnFailure] = useState(BackfillOnFailure.stop);
  const [params, setParams] = useState('');
  const [redoConfirmed, setRedoConfirmed] = useState(false);
  const [plan, setPlan] = useState<BackfillPlan | null>(null);
  // The options the plan shown was made with: a start sends only those,
  // not options changed since.
  const [plannedWith, setPlannedWith] = useState('');
  const [stale, setStale] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const validConcurrency = Number.isInteger(concurrency) && concurrency >= 1;
  const named = target.kind === 'slots';
  // The target as text, so that a new object with the same slots plans
  // nothing again.
  const targetKey = JSON.stringify(target);
  const optionsKey = JSON.stringify([
    targetKey,
    rerun,
    concurrency,
    onFailure,
    params,
  ]);

  useEffect(() => {
    if (!open || !validConcurrency) return;
    let current = true;
    const timer = setTimeout(async () => {
      try {
        const { data, error: apiError } = await client.POST(
          '/dags/{fileName}/backfills',
          {
            params: { path: { fileName }, query: { remoteNode } },
            body: {
              ...requestBody(JSON.parse(targetKey) as BackfillTarget, {
                rerun,
                concurrency,
                onFailure,
                params,
              }),
              dryRun: true,
            },
          }
        );
        if (!current) return;
        if (data) {
          setPlan(data.plan);
          setPlannedWith(optionsKey);
          setStale(false);
          setError(null);
        } else {
          setPlan(null);
          setError(messageOf(apiError, ts('Failed to plan the backfill')));
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
    rerun,
    params,
    targetKey,
    optionsKey,
    fileName,
    remoteNode,
    client,
    ts,
  ]);

  const changeOpen = (next: boolean) => {
    if (!next) {
      setPlan(null);
      setStale(false);
      setError(null);
      setRedoConfirmed(false);
    }
    onOpenChange(next);
  };

  const runs =
    plan?.slots.filter((slot) => slot.action === BackfillPlanSlotAction.run)
      .length ?? 0;
  const planned = plannedWith === optionsKey;
  const redone =
    !named && rerun === BackfillRerun.all && plan
      ? plan.runCounts.all - plan.runCounts.failed
      : 0;
  const first = plan?.slots[0]?.slot;
  const last = plan?.slots[plan.slots.length - 1]?.slot;

  const start = async () => {
    if (!plan) return;
    setSubmitting(true);
    setError(null);
    try {
      const {
        data,
        error: apiError,
        response,
      } = await client.POST('/dags/{fileName}/backfills', {
        params: { path: { fileName }, query: { remoteNode } },
        body: {
          ...requestBody(target, { rerun, concurrency, onFailure, params }),
          dryRun: false,
          revision: plan.revision,
        },
      });
      if (data?.backfill) {
        onStarted(data.backfill, !!data.existing);
        changeOpen(false);
      } else if (response.status === 409 && apiError && 'plan' in apiError) {
        setPlan((apiError as SubmitResponse).plan);
        setPlannedWith(optionsKey);
        setStale(true);
      } else {
        setError(messageOf(apiError, ts('Failed to start the backfill')));
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      <DialogContent className="sm:max-w-[540px]">
        <DialogHeader>
          <DialogTitle>{ts('Backfill')}</DialogTitle>
          <DialogDescription>
            {named
              ? ts('Runs the {count} slots you picked, oldest first.', {
                  count: target.slots.length,
                })
              : ts('Runs the past slots of the range, oldest first.')}
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4 text-sm">
          {!named && (
            <fieldset className="flex flex-col gap-1.5">
              <legend className="mb-1 font-medium">{ts('What to run')}</legend>
              {RERUNS.map((value) => (
                <label
                  key={value}
                  className="flex cursor-pointer items-center gap-2"
                >
                  <input
                    type="radio"
                    name="backfill-rerun"
                    checked={rerun === value}
                    onChange={() => {
                      setRerun(value);
                      setRedoConfirmed(false);
                    }}
                  />
                  <span>{ts(RERUN_LABEL[value])}</span>
                  {plan && (
                    <span className="tabular-nums text-muted-foreground">
                      {plan.runCounts[value]}
                    </span>
                  )}
                </label>
              ))}
            </fieldset>
          )}

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
                  name="backfill-start-on-failure"
                  checked={onFailure === option.value}
                  onChange={() => setOnFailure(option.value)}
                />
                {ts(option.label)}
              </label>
            ))}
          </fieldset>

          <label className="flex flex-col gap-1">
            <span className="font-medium">{ts('Params')}</span>
            <Input
              value={params}
              placeholder={ts("The DAG's defaults")}
              onChange={(event) => setParams(event.target.value)}
            />
          </label>

          <div className="rounded-md border bg-muted/30 px-3 py-2">
            {!plan ? (
              <span className="text-muted-foreground">
                {error ? '' : ts('Planning…')}
              </span>
            ) : runs === 0 ? (
              <span>{ts('Nothing to run: every slot is done or held.')}</span>
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
                {first && last && (
                  <span className="text-xs tabular-nums text-muted-foreground">
                    {ts('Slots {from} to {to}', {
                      from:
                        formatAbsoluteTime(first, { seconds: false }) ?? first,
                      to: formatAbsoluteTime(last, { seconds: false }) ?? last,
                    })}
                  </span>
                )}
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

          {redone > 0 && (
            <label className="flex cursor-pointer items-center gap-2">
              <Checkbox
                checked={redoConfirmed}
                onCheckedChange={(checked) =>
                  setRedoConfirmed(checked === true)
                }
              />
              {redone === 1
                ? ts('Run 1 slot that succeeded again')
                : ts('Run {count} slots that succeeded again', {
                    count: redone,
                  })}
            </label>
          )}
          {plan && !planned && !error && (
            <p role="status" className="text-xs text-muted-foreground">
              {ts('Planning…')}
            </p>
          )}
          {stale && (
            <p
              role="status"
              className="text-sm"
              style={{ color: 'var(--status-warning)' }}
            >
              {ts('The plan changed. Review it and start again.')}
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
            disabled={
              !plan ||
              !planned ||
              runs === 0 ||
              !validConcurrency ||
              submitting ||
              (redone > 0 && !redoConfirmed)
            }
            onClick={() => void start()}
          >
            {!plan
              ? ts('Start')
              : runs === 1
                ? ts('Start 1 run')
                : ts('Start {count} runs', { count: runs })}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
