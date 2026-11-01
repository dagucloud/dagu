// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ChevronDown, ChevronRight, FlaskConical } from 'lucide-react';
import {
  useId,
  useMemo,
  useState,
  type ReactElement,
  type ReactNode,
} from 'react';
import { NodeStatus } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import {
  PreconditionErrors,
  RunNodeProgress,
  RunStatusMessage,
  RuntimeConditions,
} from '@/features/dags/components/common/RunSummaryBlocks';
import {
  isEphemeralRun,
  outputsFromRunId,
  reusedTimeMs,
} from '@/features/visual-editor/run/inputProvenance';
import { useRunLinks } from '@/features/visual-editor/references/RunLinks';
import { useI18n } from '@/i18n/I18nProvider';
import dayjs from '@/lib/dayjs';
import { formatStepSpan } from '@/lib/formatStepSpan';
import { runDisplayStatus } from '@/lib/runDisplayStatus';
import { shortRunId } from '@/lib/shortRunId';
import { toneClasses } from '@/lib/statusPalette';
import { cn, FOCUS_RING } from '@/lib/utils';
import type { ApiDAGRunDetails } from '../contracts';
import { TEST_RUN_TTL_MINUTES, testRunExpiry } from '../data/testRun';
import { InputsUsed, useInputsUsed } from '../step/InputsUsed';

// The pieces of the run details popover. Each one shows only when it applies
// and can be used alone; RunDetailsPopover puts them in order.

/** One labelled row of the definition list: a muted label and its value. */
export function DetailRow({
  label,
  testId,
  children,
}: {
  label: string;
  testId?: string;
  children: ReactNode;
}): ReactElement {
  return (
    <dl
      data-testid={testId}
      className="m-0 grid min-w-0 grid-cols-[4.5rem_minmax(0,1fr)] gap-x-2 text-xs"
    >
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="m-0 min-w-0 space-y-1">{children}</dd>
    </dl>
  );
}

/** A chevron button that folds a block, closed by default. */
export function Disclosure({
  label,
  testId,
  children,
}: {
  label: string;
  testId: string;
  children: ReactNode;
}): ReactElement {
  const id = useId();
  const [open, setOpen] = useState(false);
  const Chevron = open ? ChevronDown : ChevronRight;
  return (
    <div className="min-w-0 text-xs">
      <button
        type="button"
        data-testid={`${testId}-toggle`}
        aria-expanded={open}
        aria-controls={open ? id : undefined}
        className={cn(
          'inline-flex items-center gap-1 rounded-sm font-medium text-muted-foreground hover:text-foreground',
          FOCUS_RING
        )}
        onClick={() => setOpen((value) => !value)}
      >
        <Chevron aria-hidden="true" className="size-3.5 shrink-0" />
        {label}
      </button>
      {open && (
        <div id={id} data-testid={testId} className="mt-1 min-w-0">
          {children}
        </div>
      )}
    </div>
  );
}

/**
 * The status text, the status message of the run and, when the run failed,
 * its error. The Show log button opens the Log tab of the dock.
 */
export function RunStatusSection({
  details,
  onShowLog,
}: {
  details: ApiDAGRunDetails;
  onShowLog: () => void;
}): ReactElement {
  const { ts } = useI18n();
  const display = runDisplayStatus(details);
  const colours = toneClasses(display.tone);
  return (
    <DetailRow label={ts('Status')} testId="ww-run-details-status">
      <div className="flex min-w-0 items-center gap-1.5">
        <span
          aria-hidden="true"
          className={cn('size-2 shrink-0 rounded-full', colours.dot)}
        />
        <span className={cn('font-medium', colours.text)}>
          {ts(display.text)}
        </span>
        <Button
          type="button"
          variant="link"
          size="xs"
          className="ml-auto h-auto shrink-0 p-0"
          data-testid="ww-run-details-log"
          onClick={onShowLog}
        >
          {ts('Show log')}
        </Button>
      </div>
      <RunStatusMessage status={details.status} />
      {details.error && (
        <p
          data-testid="ww-run-details-error"
          className="m-0 whitespace-pre-wrap break-words font-mono text-destructive"
        >
          {details.error}
        </p>
      )}
    </DetailRow>
  );
}

/** The steps that succeeded, as a thin bar, while the run is active. */
export function RunProgressSection({
  details,
}: {
  details: ApiDAGRunDetails;
}): ReactElement | null {
  const { ts } = useI18n();
  const nodes = details.nodes ?? [];
  if (nodes.length === 0) return null;
  const done = nodes.filter((n) => n.status === NodeStatus.Success).length;
  return (
    <DetailRow label={ts('Progress')} testId="ww-run-details-progress">
      <div
        role="progressbar"
        aria-label={ts('Run progress')}
        aria-valuemin={0}
        aria-valuemax={nodes.length}
        aria-valuenow={done}
        aria-valuetext={ts('{done} of {total} succeeded', {
          done,
          total: nodes.length,
        })}
        className="flex items-center gap-2"
      >
        <RunNodeProgress nodes={nodes} />
      </div>
    </DetailRow>
  );
}

/** The note of a test run: the server drops it a short time after its end. */
export function TestRunNote({
  details,
}: {
  details: ApiDAGRunDetails;
}): ReactElement | null {
  const { ts } = useI18n();
  if (!isEphemeralRun(details)) return null;
  const expiry = testRunExpiry(details);
  const text =
    expiry === null
      ? ts(
          'Test run, not kept: it disappears {minutes} minutes after it ends',
          {
            minutes: TEST_RUN_TTL_MINUTES,
          }
        )
      : ts('Test run, not kept: it disappears at {time}', {
          time: dayjs(expiry).format('HH:mm:ss'),
        });
  return (
    <p
      data-testid="ww-run-details-test"
      className="m-0 flex items-start gap-1.5 text-xs text-muted-foreground"
    >
      <FlaskConical aria-hidden="true" className="mt-0.5 size-3.5 shrink-0" />
      {text}
    </p>
  );
}

/**
 * What the run took from other runs: the time that its reused steps took,
 * the run that fed its inputs with a link, and the inputs used, folded.
 */
export function ReusedSection({
  details,
  onShowRun,
}: {
  details: ApiDAGRunDetails;
  /** Selects a run: the run that fed the inputs. */
  onShowRun: (runId: string) => void;
}): ReactElement | null {
  const { ts } = useI18n();
  const links = useRunLinks();
  const records = useInputsUsed(details);
  const reusedMs = useMemo(() => reusedTimeMs(details), [details]);
  const labelled = outputsFromRunId(details.labels);
  if (reusedMs === null && labelled === null && records.length === 0) {
    return null;
  }
  const reusedText =
    reusedMs === null
      ? ''
      : (formatStepSpan(
          new Date(0).toISOString(),
          new Date(reusedMs).toISOString(),
          { belowSecond: ts('<1s'), wholeSecond: true }
        ) ?? '');
  // The server stores labels in lower case, but run IDs are case sensitive:
  // the history or the record of the reused inputs gives the real ID.
  const inputsFrom =
    labelled === null
      ? null
      : (links?.resolve(labelled) ??
        details.reusedInputs?.find(
          (input) => input.runId.toLowerCase() === labelled.toLowerCase()
        )?.runId ??
        labelled);
  return (
    <DetailRow label={ts('Reused')} testId="ww-run-details-reused">
      {reusedMs !== null && (
        <p
          data-testid="ww-run-reused-time"
          className="m-0"
          title={ts(
            'Time that the reused steps took in their source runs. It is not part of the duration of this run.'
          )}
        >
          {ts('Reused time: {time}', { time: reusedText })}
        </p>
      )}
      {inputsFrom && (
        <div
          data-testid="ww-run-inputs-from"
          className="flex min-w-0 items-center gap-1.5"
        >
          <span className="min-w-0 truncate font-mono" title={inputsFrom}>
            {ts('Inputs from run {id}', { id: shortRunId(inputsFrom) })}
          </span>
          <Button
            type="button"
            variant="link"
            size="xs"
            className="h-auto shrink-0 p-0"
            onClick={() => onShowRun(inputsFrom)}
          >
            {ts('Show run')}
          </Button>
        </div>
      )}
      {records.length > 0 && (
        <Disclosure
          label={ts('Inputs used ({count})', { count: records.length })}
          testId="ww-run-details-inputs"
        >
          <InputsUsed details={details} heading={false} />
        </Disclosure>
      )}
    </DetailRow>
  );
}

/** The runtime conditions that are not true and the unmet preconditions. */
export function ConditionsSection({
  details,
}: {
  details: ApiDAGRunDetails;
}): ReactElement {
  return (
    <div className="min-w-0 space-y-2 text-xs empty:hidden">
      <RuntimeConditions conditions={details.conditions} />
      <PreconditionErrors preconditions={details.preconditions} />
    </div>
  );
}

/** The parameters of the run, folded. */
export function ParamsSection({
  details,
}: {
  details: ApiDAGRunDetails;
}): ReactElement | null {
  const { ts } = useI18n();
  if (!details.params) return null;
  return (
    <Disclosure label={ts('Parameters')} testId="ww-run-details-params">
      <div
        data-testid="ww-run-params"
        className="max-h-[120px] overflow-y-auto whitespace-pre-wrap break-all rounded-md border bg-accent px-3 py-1.5 font-mono text-xs text-foreground"
      >
        {details.params}
      </div>
    </Disclosure>
  );
}
