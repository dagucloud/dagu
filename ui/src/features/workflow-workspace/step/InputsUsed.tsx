// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useMemo, useState, type ReactElement } from 'react';
import { type components } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { isSubDAGRun } from '@/features/dags/hooks/useStepLogQuery';
import { useStepOutputs } from '@/features/visual-editor/api/useStepOutputs';
import { InputMark } from '@/features/visual-editor/references/InputMark';
import { useRunLinks } from '@/features/visual-editor/references/RunLinks';
import { usedValues } from '@/features/visual-editor/references/UpstreamDataPanel';
import {
  runInputProvenance,
  type InputProvenance,
} from '@/features/visual-editor/run/inputProvenance';
import {
  useCacheScope,
  useOutputCacheEntries,
} from '@/features/visual-editor/run/useOutputCacheEntries';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import { shortRunId } from '@/lib/shortRunId';
import type { ApiDAGRunDetails } from '../contracts';
import { useWorkspaceData } from '../data/context';
import { StepOutputRows } from './StepOutputRows';

type OutputValue = components['schemas']['StepOutputValue'];

/** The longest first value that a collapsed row shows. */
const PREVIEW_CHARS = 60;

/** The steps that a run reused, in the order of its nodes. */
export function useInputsUsed(
  details: ApiDAGRunDetails | null | undefined
): InputProvenance[] {
  return useMemo(() => {
    const found = runInputProvenance(details);
    const order = new Map(
      (details?.nodes ?? []).map((n, i) => [n.step.name, i] as const)
    );
    return [...found.values()].sort(
      (a, b) => (order.get(a.step) ?? 0) - (order.get(b.step) ?? 0)
    );
  }, [details]);
}

/** True when two records have the same source: the mark would say the same. */
export function sameSource(a: InputProvenance, b: InputProvenance): boolean {
  return a.source === b.source && a.runId === b.runId && a.stale === b.stale;
}

/** The records of a list without a repeated source, in order. */
export function distinctSources(
  records: readonly InputProvenance[]
): InputProvenance[] {
  const found: InputProvenance[] = [];
  for (const record of records) {
    if (!found.some((other) => sameSource(other, record))) found.push(record);
  }
  return found;
}

function compact(text: string): string {
  const flat = text.replace(/\s+/g, ' ').trim();
  return flat.length > PREVIEW_CHARS
    ? `${flat.slice(0, PREVIEW_CHARS)}…`
    : flat;
}

/** The values of one reused step: the run's own data, else what the cache kept. */
function useReusedValues(
  details: ApiDAGRunDetails,
  record: InputProvenance
): { outputs: OutputValue[]; outputVariable?: OutputValue } | null {
  const { fileName } = useWorkspaceData('InputsUsed');
  const readable = !isSubDAGRun(details);
  const { data } = useStepOutputs(
    details.name,
    readable ? details.dagRunId : null,
    readable ? record.step : null
  );
  const scope = useCacheScope(fileName);
  const entries = useOutputCacheEntries(scope);
  if (data && (data.outputs.length > 0 || data.outputVariable)) {
    return { outputs: data.outputs, outputVariable: data.outputVariable };
  }
  const kept = usedValues(record, entries[record.step]);
  if (!kept) return null;
  return {
    outputs: Object.entries(kept).map(([name, value]) => ({
      name,
      value,
      truncated: false,
      size: value.length,
    })),
  };
}

/**
 * The one line above the list: where the values come from, with the mark and
 * the link to the run. The rows do not repeat it.
 */
function SourceLine({ source }: { source: InputProvenance }): ReactElement {
  const { ts } = useI18n();
  const links = useRunLinks();
  const target = links && source.runId ? links.resolve(source.runId) : null;
  const time =
    source.capturedAt === null ? null : formatAbsoluteTime(source.capturedAt);
  const id = shortRunId(source.runId ?? '');
  let text: string;
  if (source.runId) {
    text = time
      ? ts('Values from run {id}, {time}', { id, time })
      : ts('Values from run {id}', { id });
  } else {
    text = time
      ? ts('Values from the cache, {time}', { time })
      : ts('Values from the cache');
  }
  return (
    <div
      data-testid="inputs-used-source"
      className="flex min-w-0 items-center gap-1.5 text-muted-foreground"
    >
      <InputMark
        provenance={source}
        variant="icon"
        testId="inputs-used-source-mark"
        onSelectRun={
          links && target !== null ? () => links.select(target) : undefined
        }
      />
      <span className="min-w-0 truncate">{text}</span>
      {target !== null && source.runId && (
        <Button
          type="button"
          variant="link"
          size="xs"
          className="h-auto shrink-0 p-0"
          data-testid="inputs-used-source-show"
          onClick={() => links?.select(target)}
        >
          {ts('Show run')}
        </Button>
      )}
    </div>
  );
}

function InputRow({
  details,
  record,
  listSource,
}: {
  details: ApiDAGRunDetails;
  record: InputProvenance;
  /** The source of the list line: a row shows its mark only when it differs. */
  listSource: InputProvenance;
}): ReactElement {
  const { ts } = useI18n();
  const links = useRunLinks();
  const [open, setOpen] = useState(false);
  const values = useReusedValues(details, record);
  const first = values?.outputVariable ?? values?.outputs[0];
  // The source run is a link when the history holds it.
  const target = links && record.runId ? links.resolve(record.runId) : null;
  return (
    <li
      data-testid={`inputs-used-${record.step}`}
      className="min-w-0 space-y-1 rounded border border-border px-2 py-1"
    >
      <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
        <span className="font-mono font-medium">{record.step}</span>
        {!sameSource(record, listSource) && (
          <InputMark
            provenance={record}
            testId={`inputs-used-mark-${record.step}`}
            onSelectRun={
              links && target !== null ? () => links.select(target) : undefined
            }
          />
        )}
        {first && !open && (
          <span
            data-testid={`inputs-used-first-${record.step}`}
            className="min-w-0 flex-1 truncate font-mono text-muted-foreground"
            title={first.value}
          >
            {first.name}: {compact(first.value)}
          </span>
        )}
        {values && (
          <Button
            type="button"
            variant="link"
            size="xs"
            className="ml-auto h-auto shrink-0 p-0"
            aria-expanded={open}
            data-testid={`inputs-used-toggle-${record.step}`}
            onClick={() => setOpen((v) => !v)}
          >
            {open ? ts('Hide values') : ts('Show values')}
          </Button>
        )}
      </div>
      {open && values && (
        <StepOutputRows
          outputs={values.outputs}
          outputVariable={values.outputVariable}
        />
      )}
    </li>
  );
}

/**
 * The inputs that a run used: one line with the source (run, cache or pin),
 * then each previous step that it did not execute with its values, collapsed
 * to the first one with a Show values toggle. A row has a mark of its own
 * only when its source differs from the line. Nothing when the run reused
 * no step.
 */
export function InputsUsed({
  details,
  heading = true,
}: {
  details: ApiDAGRunDetails;
  /** False when the host has a title of its own for the block. */
  heading?: boolean;
}): ReactElement | null {
  const { ts } = useI18n();
  const records = useInputsUsed(details);
  if (records.length === 0) return null;
  return (
    <section
      data-testid="ww-run-inputs-used"
      className="min-w-0 space-y-1 text-xs"
    >
      {heading && (
        <h3 className="font-medium text-muted-foreground">
          {ts('Inputs used')}
        </h3>
      )}
      <SourceLine source={records[0]!} />
      <ul className="m-0 min-w-0 list-none space-y-1 p-0">
        {records.map((record) => (
          <InputRow
            key={record.step}
            details={details}
            record={record}
            listSource={records[0]!}
          />
        ))}
      </ul>
    </section>
  );
}
