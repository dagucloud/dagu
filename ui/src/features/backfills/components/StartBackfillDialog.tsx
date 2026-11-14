// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { AlertTriangle, ChevronDown, ChevronRight } from 'lucide-react';
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
import { useCanManageProfiles } from '@/contexts/AuthContext';
import {
  DAGParamsFields,
  useDAGParamsForm,
} from '@/features/dags/components/dag-execution/DAGParamsForm';
import { useProfiles } from '@/features/dags/hooks/useProfiles';
import { useClient, useQuery } from '@/hooks/api';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import { formatRoughDurationMs } from '@/lib/formatDuration';
import type { Backfill } from '../lib/backfillState';
import { RERUN_LABEL } from '../lib/backfillState';
import { BackfillRangePicker } from './BackfillRangePicker';

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
  /**
   * Lets the user pick the range, starting from target, as when the panel
   * opens from the Run menu rather than from slots already chosen.
   */
  pickRange?: boolean;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** The backfill that started, or the open one that has the same options. */
  onStarted: (backfill: Backfill, existing: boolean) => void;
};

/** How long the options rest before the plan is asked for again. */
const REPLAN_DELAY_MS = 400;

/** The DAG's details are read once a time the panel opens. */
const READ_ONCE = {
  revalidateOnFocus: false,
  revalidateIfStale: false,
  refreshInterval: 0,
} as const;

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
  /** The runtime profile, or empty for the DAG's default. */
  profile: string;
}

/** The request that plans or starts a backfill of target with options. */
function requestBody(
  target: BackfillTarget,
  { rerun, concurrency, onFailure, params, profile }: StartOptions
): BackfillRequest {
  return {
    ...(target.kind === 'slots'
      ? { slots: [...target.slots] }
      : { from: target.from, ...(target.to ? { to: target.to } : {}), rerun }),
    maxConcurrency: concurrency,
    onFailure,
    ...(params.trim() ? { params: params.trim() } : {}),
    ...(profile ? { profile } : {}),
  };
}

/**
 * The first and last date a plan's runs load: the values of the entry it
 * selects by, else of the DAG's first date. Null when the DAG has no dates.
 */
export function loadsRange(
  plan: BackfillPlan
): { entry: string; from: string; to: string } | null {
  const runs = plan.slots.filter(
    (slot) => slot.action === BackfillPlanSlotAction.run
  );
  const entry =
    plan.by ?? runs.find((slot) => slot.dates?.length)?.dates?.[0]?.name;
  if (!entry) return null;
  const values = runs.flatMap((slot) => {
    const date = slot.dates?.find((d) => d.name === entry);
    if (!date) return [];
    return date.list ? (date.values ?? []) : date.value ? [date.value] : [];
  });
  if (values.length === 0) return null;
  return { entry, from: values[0]!, to: values[values.length - 1]! };
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
 * of starting. The DAG's params form sends params only once one is changed,
 * so each run otherwise takes its schedule's params, defaults, and evals.
 */
export function StartBackfillDialog({
  fileName,
  remoteNode,
  target,
  pickRange = false,
  open,
  onOpenChange,
  onStarted,
}: Props) {
  const { ts } = useI18n();
  const client = useClient();
  const canManageProfiles = useCanManageProfiles();
  const { activeProfiles } = useProfiles({
    enabled: open && canManageProfiles,
    remoteNode,
  });
  const [picked, setPicked] = useState<BackfillTarget | null>(target);
  const chosen = pickRange ? picked : target;
  const [profile, setProfile] = useState('');
  const [showOptions, setShowOptions] = useState(false);
  const [rerun, setRerun] = useState(BackfillRerun.none);
  const [concurrency, setConcurrency] = useState(1);
  const [onFailure, setOnFailure] = useState(BackfillOnFailure.stop);
  const { data: details } = useQuery(
    '/dags/{fileName}',
    open ? { params: { path: { fileName }, query: { remoteNode } } } : null,
    READ_ONCE
  );
  const paramsForm = useDAGParamsForm(details?.dag, open);
  const params = paramsForm.serialize(true);
  // The plan and count of succeeded slots a confirmation was given for: a
  // plan that runs other slots asks again.
  const [confirmedRedo, setConfirmedRedo] = useState<string | null>(null);
  const [plan, setPlan] = useState<BackfillPlan | null>(null);
  // The options the plan shown was made with: a start sends only those,
  // not options changed since.
  const [plannedWith, setPlannedWith] = useState('');
  const [stale, setStale] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const validConcurrency = Number.isInteger(concurrency) && concurrency >= 1;
  const named = chosen?.kind === 'slots';
  // The target as text, so that a new object with the same slots plans
  // nothing again.
  const targetKey = JSON.stringify(chosen);
  const optionsKey = JSON.stringify([
    targetKey,
    rerun,
    concurrency,
    onFailure,
    params,
    profile,
  ]);

  useEffect(() => {
    if (!open || !validConcurrency || targetKey === 'null') return;
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
                profile,
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
    profile,
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
      setConfirmedRedo(null);
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
  const redoKey = `${plan?.revision ?? ''}/${redone}`;
  const redoConfirmed = confirmedRedo === redoKey;
  const first = plan?.slots[0]?.slot;
  const last = plan?.slots[plan.slots.length - 1]?.slot;
  const loads = plan ? loadsRange(plan) : null;

  const start = async () => {
    if (!plan || !chosen) return;
    if (!paramsForm.validate()) {
      setShowOptions(true);
      setError(ts('Fix the highlighted params before starting.'));
      return;
    }
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
          ...requestBody(chosen, {
            rerun,
            concurrency,
            onFailure,
            params,
            profile,
          }),
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
            {chosen?.kind === 'slots'
              ? ts('Runs the {count} slots you picked, oldest first.', {
                  count: chosen.slots.length,
                })
              : ts('Runs the past slots of the range, oldest first.')}
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4 text-sm">
          {pickRange && (
            <BackfillRangePicker
              fileName={fileName}
              remoteNode={remoteNode}
              onChange={(next) => {
                setPicked(next);
                setConfirmedRedo(null);
              }}
            />
          )}
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
                      setConfirmedRedo(null);
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

          <button
            type="button"
            aria-expanded={showOptions}
            className="flex items-center gap-1 self-start font-medium outline-none focus-visible:ring-2 focus-visible:ring-ring"
            onClick={() => setShowOptions((shown) => !shown)}
          >
            {showOptions ? (
              <ChevronDown className="h-4 w-4" aria-hidden="true" />
            ) : (
              <ChevronRight className="h-4 w-4" aria-hidden="true" />
            )}
            {ts('Options')}
            {!showOptions && (
              <span className="font-normal text-muted-foreground">
                {onFailure === BackfillOnFailure.stop
                  ? ts('{count} at a time, stop after a failed slot', {
                      count: concurrency,
                    })
                  : ts('{count} at a time, continue after a failed slot', {
                      count: concurrency,
                    })}
              </span>
            )}
          </button>
          {showOptions && (
            <div className="flex flex-col gap-4 border-l pl-4">
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
                    label:
                      'Continue: for slots that do not depend on each other',
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

              {paramsForm.count > 0 && (
                <fieldset className="flex flex-col gap-3">
                  <legend className="mb-1 font-medium">{ts('Params')}</legend>
                  <DAGParamsFields form={paramsForm} disabled={submitting} />
                </fieldset>
              )}

              {activeProfiles.length > 0 && (
                <label className="flex flex-col gap-1">
                  <span className="font-medium">{ts('Profile')}</span>
                  <select
                    className="h-8 w-56 rounded-md border bg-background px-2"
                    value={profile}
                    onChange={(event) => setProfile(event.target.value)}
                  >
                    <option value="">{ts("The DAG's default profile")}</option>
                    {activeProfiles.map((item) => (
                      <option key={item.name} value={item.name}>
                        {item.name}
                      </option>
                    ))}
                  </select>
                </label>
              )}
            </div>
          )}

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
                {loads && (
                  <span className="text-xs tabular-nums text-muted-foreground">
                    {ts('Loads {from} to {to} ({entry})', loads)}
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
                  setConfirmedRedo(checked === true ? redoKey : null)
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
              !chosen ||
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
