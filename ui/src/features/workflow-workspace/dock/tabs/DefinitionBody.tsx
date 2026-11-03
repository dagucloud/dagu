// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { editor as MonacoEditor } from 'monaco-editor';
import {
  lazy,
  Suspense,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react';
import { Button } from '@/components/ui/button';
import { ToggleButton, ToggleGroup } from '@/components/ui/toggle-group';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { YamlFileBody } from '@/features/dags/components/dag-editor/YamlFilePanel';
import {
  parseDagDocLenient,
  resolveStepNames,
} from '@/features/visual-editor/doc';
import { DiffLayoutToggle } from '@/features/visual-editor/run/DiffLayoutToggle';
import { lineDiffStats } from '@/features/visual-editor/run/lineDiffStats';
import { specTextDiffers } from '@/features/visual-editor/run/runSpecDiff';
import { ACTIVE_BLOCK_CLASS } from '@/features/visual-editor/useYamlEditorSync';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import { useI18n } from '@/i18n/I18nProvider';
import { readableToneText } from '@/lib/statusPalette';
import { cn, FOCUS_RING } from '@/lib/utils';
import type { ApiDAGRunDetails } from '../../contracts';
import type { RunRef } from '../../data/types';
import { runDagDetails } from '../../executions/runDagDetails';
import { useRunSpec } from '../../executions/useRunSpec';
import { useWorkspaceUrl } from '../../state/workspaceUrl';
import { useDockStep } from '../useDockStep';
import { RunTabEmpty } from './runTabBodies';
import type { DockTabBodyProps } from './types';

// The diff viewer loads with the first Changes view, not with the dock.
const TextDiffView = lazy(() =>
  import('@/features/visual-editor/run/TextDiffView').then((m) => ({
    default: m.TextDiffView,
  }))
);

type DefinitionView = 'yaml' | 'changes';

type CodeEditor = MonacoEditor.IStandaloneCodeEditor;

/**
 * The Definition tab: the YAML that the selected run used, read-only, and
 * its difference from the saved definition. The step that the Run panel
 * shows is lit in the YAML.
 */
export function DefinitionBody({ run, fileName, dag }: DockTabBodyProps) {
  const { ts } = useI18n();
  if (!run.ref) {
    return <RunTabEmpty>{ts('This workflow has not run yet.')}</RunTabEmpty>;
  }
  // One mount per run: the view and the editor start over for each run.
  return (
    <RunDefinition
      key={run.ref.runId}
      runRef={run.ref}
      details={run.details}
      fileName={fileName}
      dagName={dag.name}
    />
  );
}

interface RunDefinitionProps {
  runRef: RunRef;
  details: ApiDAGRunDetails | null;
  fileName: string;
  dagName: string;
}

function RunDefinition({
  runRef,
  details,
  fileName,
  dagName,
}: RunDefinitionProps) {
  const { ts } = useI18n();
  const remoteNode = useRemoteNode();
  const [url, setUrl] = useWorkspaceUrl();
  const step = useDockStep();
  const spec = useRunSpec(runRef, fileName);
  // Without a stored definition useRunSpec gives the saved one: the tab
  // never shows it as the definition of the run.
  const runText = spec.missing ? null : spec.text;
  const savedQuery = useQuery(
    '/dags/{fileName}/spec',
    whenEnabled(runText !== null, {
      params: { path: { fileName }, query: { remoteNode } },
    })
  );
  const saved = savedQuery.data?.spec;
  const changed =
    runText !== null && saved !== undefined && specTextDiffers(runText, saved);
  const stats = useMemo(
    () =>
      changed && runText !== null && saved !== undefined
        ? lineDiffStats(runText, saved)
        : null,
    [changed, runText, saved]
  );

  const [view, setView] = useState<DefinitionView>('yaml');
  const [split, setSplit] = useState(true);
  const shownView: DefinitionView = changed ? view : 'yaml';
  const [editor, setEditor] = useState<CodeEditor | null>(null);
  useStepHighlight(editor, runText, step, details, dagName);

  const onCanvas = url.def === 'run';
  const modelUri = `inmemory://dagu/${encodeURIComponent(remoteNode)}/dag-runs/${encodeURIComponent(runRef.name)}/${encodeURIComponent(runRef.runId)}/definition.yaml`;

  let status: ReactNode = null;
  if (runText !== null && saved !== undefined) {
    status = changed ? (
      <>
        <span className={cn('font-medium', readableToneText('warning'))}>
          {ts('Changed since this run')}
        </span>
        {stats && (
          <span className="ml-2 text-muted-foreground">
            {ts('{added} added, {removed} removed', {
              added: stats.added,
              removed: stats.removed,
            })}
          </span>
        )}
      </>
    ) : (
      <span className="text-muted-foreground">
        {ts('Same as the saved definition')}
      </span>
    );
  }

  let content: ReactNode;
  if (spec.missing) {
    content = (
      <RunTabEmpty>{ts('This run has no stored definition.')}</RunTabEmpty>
    );
  } else if (spec.error !== null) {
    content = (
      <div role="alert" className="p-3 text-sm text-destructive">
        {ts('Failed to load DAG spec:')} {spec.error || ts('Unknown error')}
      </div>
    );
  } else if (runText === null) {
    content = <RunTabEmpty>{ts('Loading...')}</RunTabEmpty>;
  } else if (shownView === 'changes' && saved !== undefined) {
    content = (
      <Suspense fallback={null}>
        <TextDiffView
          original={runText}
          modified={saved}
          originalTitle={ts('This run')}
          modifiedTitle={ts('Saved version')}
          split={split}
        />
      </Suspense>
    );
  } else {
    content = (
      <YamlFileBody
        text={runText}
        readOnly
        modelUri={modelUri}
        changeBaseline={changed ? saved : undefined}
        showDocsButton={false}
        inFrame
        onEditorMount={setEditor}
      />
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex h-8 shrink-0 items-center gap-2 border-b border-border px-2 text-xs">
        <div className="min-w-0 flex-1 truncate">{status}</div>
        {shownView === 'changes' && (
          <DiffLayoutToggle split={split} onSplitChange={setSplit} />
        )}
        {changed && (
          <ToggleGroup aria-label={ts('View')}>
            <ToggleButton
              value="yaml"
              groupValue={shownView}
              onClick={() => setView('yaml')}
              className={cn('h-6 px-2', FOCUS_RING)}
            >
              {ts('YAML')}
            </ToggleButton>
            <ToggleButton
              value="changes"
              groupValue={shownView}
              onClick={() => setView('changes')}
              className={cn('h-6 px-2', FOCUS_RING)}
            >
              {ts('Changes')}
            </ToggleButton>
          </ToggleGroup>
        )}
        <Button
          type="button"
          variant="ghost"
          size="xs"
          aria-pressed={onCanvas}
          onClick={() => setUrl({ def: onCanvas ? null : 'run' })}
        >
          {ts(onCanvas ? 'Back to the current definition' : 'Open on canvas')}
        </Button>
      </div>
      <div className="flex min-h-0 flex-1 flex-col">{content}</div>
    </div>
  );
}

/**
 * Lights the lines of `step` in the YAML and scrolls them into view. The
 * names are those of the run, so an unnamed step is found too.
 */
function useStepHighlight(
  editor: CodeEditor | null,
  text: string | null,
  step: string | null,
  details: ApiDAGRunDetails | null,
  dagName: string
) {
  const model = useMemo(
    () => (text === null ? null : parseDagDocLenient(text)),
    [text]
  );
  const range = useMemo(() => {
    if (model === null || step === null) return null;
    const names = resolveStepNames(
      model,
      details ? runDagDetails(details, dagName) : undefined
    );
    const key = names.byName[step];
    return (
      [...model.steps, ...model.handlers].find((s) => s.key === key)?.range ??
      null
    );
  }, [model, step, details, dagName]);
  // Lines, not the range: the details of a live run change on each poll,
  // and the view must not scroll back while the user reads.
  const start = range?.startLine ?? null;
  const end = range?.endLine ?? null;

  useEffect(() => {
    if (!editor || start === null || end === null) return;
    const lit = editor.createDecorationsCollection([
      {
        range: {
          startLineNumber: start,
          startColumn: 1,
          endLineNumber: end,
          endColumn: 1,
        },
        options: { isWholeLine: true, className: ACTIVE_BLOCK_CLASS },
      },
    ]);
    editor.revealLinesInCenter(start, end);
    return () => lit.clear();
  }, [editor, start, end]);
}
