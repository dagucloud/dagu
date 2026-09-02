// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import ConfirmModal from '@/components/ui/confirm-dialog';
import { useSimpleToast } from '@/components/ui/simple-toast';
import { useCanExecuteForWorkspace } from '@/contexts/AuthContext';
import { useConfig } from '@/contexts/ConfigContext';
import { useClient } from '@/hooks/api';
import { AlertTriangle, Play } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { parse as parseYAML } from 'yaml';
import { DagStatusChip } from './DagStatusChip';
import { useWikiLive } from './context';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';

type RunMode = 'start' | 'enqueue';

type BlockConfig = {
  dag: string;
  label: string;
  params: Record<string, string>;
  confirm: string;
  mode: RunMode;
  singleton: boolean;
};

const ALLOWED_KEYS = new Set([
  'dag',
  'label',
  'params',
  'confirm',
  'mode',
  'singleton',
]);

// A runbook action must fail loudly at authoring time, not at click time:
// unknown keys and invalid values reject the whole block.
function parseBlock(source: string): BlockConfig | string {
  let raw: unknown;
  try {
    raw = parseYAML(source);
  } catch {
    return 'Invalid YAML in dagu-run block';
  }
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) {
    return 'dagu-run block must be a YAML mapping with a dag key';
  }
  const record = raw as Record<string, unknown>;
  for (const key of Object.keys(record)) {
    if (!ALLOWED_KEYS.has(key)) {
      return `Unknown dagu-run key: ${key}`;
    }
  }
  const dag = record.dag;
  if (typeof dag !== 'string' || dag.trim() === '') {
    return 'dagu-run block requires a dag name';
  }
  const mode = record.mode ?? 'start';
  if (mode !== 'start' && mode !== 'enqueue') {
    return 'mode must be start or enqueue';
  }
  const params: Record<string, string> = {};
  if (record.params !== undefined) {
    if (
      typeof record.params !== 'object' ||
      record.params === null ||
      Array.isArray(record.params)
    ) {
      return 'params must be a mapping of name to value';
    }
    for (const [key, value] of Object.entries(record.params)) {
      if (value !== null && typeof value === 'object') {
        return `param ${key} must be a scalar value`;
      }
      params[key] = value === null ? '' : String(value);
    }
  }
  if (record.singleton !== undefined && typeof record.singleton !== 'boolean') {
    return 'singleton must be a boolean';
  }
  return {
    dag: dag.trim(),
    label: typeof record.label === 'string' ? record.label : '',
    params,
    confirm: typeof record.confirm === 'string' ? record.confirm : '',
    mode,
    singleton: record.singleton === true,
  };
}

// Matches the run dialog's convention: a JSON array of single-key objects.
export function serializeRunParams(params: Record<string, string>): string {
  const items = Object.entries(params).map(([name, value]) => ({
    [name]: value,
  }));
  return items.length === 0 ? '' : JSON.stringify(items);
}

function ErrorCard({ message }: { message: string }) {
  return (
    <div className="my-2 flex items-center gap-2 rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2 text-xs text-destructive">
      <AlertTriangle className="h-3.5 w-3.5 shrink-0" />
      {message}
    </div>
  );
}

function ParamsTable({ params }: { params: Record<string, string> }) {
  const entries = Object.entries(params);
  if (entries.length === 0) return null;
  return (
    <table className="text-xs">
      <tbody>
        {entries.map(([name, value]) => (
          <tr key={name}>
            <td className="pr-3 py-0.5 font-mono text-muted-foreground">
              {name}
            </td>
            <td className="py-0.5 font-mono">{value}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

type RunState =
  | { phase: 'idle' }
  | { phase: 'confirming' }
  | { phase: 'posting' }
  | { phase: 'started'; dagRunId: string; dagName: string }
  | { phase: 'error' };

type Props = {
  source: string;
};

/** Renders a ```dagu-run fenced block: a curated, fixed-params run action. */
export function DaguRunBlock({ source }: Props) {
  const live = useWikiLive();
  const client = useClient();
  const config = useConfig();
  const { showToast } = useSimpleToast();
  const { ts } = useI18n();
  const parsed = useMemo(() => parseBlock(source), [source]);
  const dagRef = typeof parsed === 'string' ? null : parsed.dag;
  const canExecuteWorkspace = useCanExecuteForWorkspace(
    live?.workspace ?? null
  );
  const [runState, setRunState] = useState<RunState>({ phase: 'idle' });

  useEffect(() => {
    if (!live || !dagRef) return;
    return live.registerRef(dagRef);
  }, [live, dagRef]);

  if (typeof parsed === 'string') {
    return <ErrorCard message={parsed} />;
  }

  const label = parsed.label || `Run ${parsed.dag}`;
  const actionLabel = ts(parsed.mode === 'enqueue' ? 'Enqueue' : 'Run');

  // Outside a Wiki page (no provider) the block is an inert summary.
  if (!live) {
    return (
      <div className="my-2 rounded-md border border-border px-3 py-2">
        <div className="flex items-center gap-1.5 text-xs font-medium">
          <Play className="h-3.5 w-3.5 text-muted-foreground" />
          {label}
        </div>
        <ParamsTable params={parsed.params} />
      </div>
    );
  }

  const lookup = live.lookup(parsed.dag);
  const runEnabled = config.permissions.runDags;
  const canExecute = canExecuteWorkspace && lookup.state === 'found';
  const posting = runState.phase === 'posting';

  const execute = async () => {
    if (lookup.state !== 'found') return;
    setRunState({ phase: 'posting' });
    const body: { params: string; singleton?: boolean } = {
      params: serializeRunParams(parsed.params),
    };
    if (parsed.singleton) {
      body.singleton = true;
    }
    const request = {
      params: {
        path: { fileName: lookup.fileName },
        query: { remoteNode: live.remoteNode },
      },
      body,
    };
    const { data, error } = await (parsed.mode === 'enqueue'
      ? client.POST('/dags/{fileName}/enqueue', request)
      : client.POST('/dags/{fileName}/start', request));
    if (error || !data?.dagRunId) {
      setRunState({ phase: 'error' });
      showToast(error?.message || 'Failed to start DAG run');
      return;
    }
    setRunState({
      phase: 'started',
      dagRunId: data.dagRunId,
      dagName: lookup.dagName,
    });
    showToast(
      parsed.mode === 'enqueue' ? 'DAG run enqueued' : 'DAG run started'
    );
  };

  return (
    <div className="my-2 rounded-md border border-border px-3 py-2">
      <div className="flex items-center gap-2">
        <Play className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
        <span className="text-xs font-medium">{label}</span>
        <DagStatusChip dagRef={parsed.dag} label="" />
        <div className="flex-1" />
        {runEnabled && (
          <button
            type="button"
            disabled={!canExecute || posting}
            onClick={() => setRunState({ phase: 'confirming' })}
            title={
              lookup.state === 'not-found'
                ? ts('DAG not found: {name}', { name: parsed.dag })
                : !canExecuteWorkspace
                  ? ts('Requires operator access in this workspace')
                  : undefined
            }
            className="flex items-center gap-1 px-2 py-0.5 text-xs rounded-md border border-border bg-primary text-primary-foreground disabled:opacity-50 disabled:cursor-not-allowed hover:opacity-90"
          >
            <Play className="h-3 w-3" />
            {posting ? <I18nText text="Starting…" /> : actionLabel}
          </button>
        )}
      </div>
      <ParamsTable params={parsed.params} />
      {runState.phase === 'started' && (
        <div className="mt-1 text-xs">
          <Link
            to={`/dag-runs/${encodeURIComponent(runState.dagName)}/${encodeURIComponent(runState.dagRunId)}`}
            className="text-primary hover:underline"
          >
            <I18nText text={'View run →'} />
          </Link>
        </div>
      )}

      <ConfirmModal
        title={label}
        buttonText={actionLabel}
        visible={runState.phase === 'confirming'}
        dismissModal={() => setRunState({ phase: 'idle' })}
        onSubmit={() => void execute()}
      >
        <div className="space-y-2 text-sm">
          {parsed.confirm && <p>{parsed.confirm}</p>}
          <div className="flex items-center gap-2 text-xs">
            <I18nText text={'Target:'} /> <DagStatusChip dagRef={parsed.dag} />
          </div>
          <ParamsTable params={parsed.params} />
        </div>
      </ConfirmModal>
    </div>
  );
}
