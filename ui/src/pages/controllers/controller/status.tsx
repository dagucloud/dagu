// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import * as React from 'react';
import { Edit3, Play, Send, Square } from 'lucide-react';
import { Link, useParams } from 'react-router-dom';

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import LoadingIndicator from '@/components/ui/loading-indicator';
import RelativeTime from '@/components/ui/relative-time';
import { Textarea } from '@/components/ui/textarea';
import { AppBarContext } from '@/contexts/AppBarContext';
import {
  useCanExecuteForWorkspace,
  useCanWriteForWorkspace,
} from '@/contexts/AuthContext';
import {
  useControllerAPI,
  useControllerDetail,
} from '@/features/controllers/api';
import { ControllerContext } from '@/features/controllers/components/ControllerContext';
import { ControllerGraph } from '@/features/controllers/components/ControllerGraph';
import { ControllerPageHeader } from '@/features/controllers/components/ControllerPageHeader';
import { ControllerPromptDialog } from '@/features/controllers/components/ControllerPromptDialog';
import { ControllerStatusChip } from '@/features/controllers/components/ControllerStatusChip';
import { buildDAGRunPageURL } from '@/features/dag-runs/lib/dagRunUrls';
import {
  MAX_CONTROLLER_PROMPT_BYTES,
  utf8ByteLength,
  validateControllerPrompt,
} from '@/features/controllers/constraints';
import {
  ControllerStatus,
  canStartController,
  isControllerStatusActive,
} from '@/features/controllers/types';
import { useControllerMutation } from '@/features/controllers/useControllerMutation';
import { workspaceNameFromLabels } from '@/lib/workspace';

function Metric({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <Card>
      <CardContent className="py-4">
        <div className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
          {label}
        </div>
        <div className="mt-2 text-base font-semibold">{children}</div>
      </CardContent>
    </Card>
  );
}

export default function ControllerStatusPage() {
  const { id } = useParams<{ id: string }>();
  const appBar = React.useContext(AppBarContext);
  const api = useControllerAPI();
  const { data: detail, error, isLoading, mutate } = useControllerDetail(id);
  const [startOpen, setStartOpen] = React.useState(false);
  const [prompt, setPrompt] = React.useState('');
  const {
    pending,
    error: actionError,
    run: execute,
  } = useControllerMutation(
    mutate,
    'The action succeeded, but the latest Controller status could not be loaded.',
    id ?? ''
  );
  const promptBytes = utf8ByteLength(prompt);
  const promptError = validateControllerPrompt(prompt);
  const workspace = workspaceNameFromLabels(detail?.definition.labels);
  const canExecute = useCanExecuteForWorkspace(workspace);
  const canWrite = useCanWriteForWorkspace(workspace);

  React.useEffect(() => {
    if (detail) appBar.setTitle(detail.definition.name);
  }, [appBar, detail]);

  React.useLayoutEffect(() => {
    setStartOpen(false);
    setPrompt('');
  }, [id]);

  React.useEffect(() => {
    setPrompt('');
  }, [detail?.runtime.turnCount, detail?.runtime.waitingQuestion]);

  if (isLoading && !detail) return <LoadingIndicator />;
  if (error || !detail) {
    return (
      <Alert variant="destructive">
        <AlertTitle>Unable to load Controller</AlertTitle>
        <AlertDescription>
          {error?.message ?? 'Controller not found'}
        </AlertDescription>
      </Alert>
    );
  }

  const runtime = detail.runtime;
  const active = isControllerStatusActive(runtime.status, runtime.finishedAt);
  const waitingForPrompt =
    runtime.status === ControllerStatus.Waiting &&
    !runtime.activeDAGRun &&
    Boolean(runtime.waitingQuestion);
  const waitingForApproval =
    runtime.status === ControllerStatus.Waiting &&
    Boolean(runtime.activeDAGRun);
  const startable = canStartController(runtime.status, runtime.finishedAt);
  const refs = [
    ...(runtime.activeDAGRun ? [runtime.activeDAGRun] : []),
    ...runtime.dagRunRefs,
  ].filter(
    (run, index, items) =>
      items.findIndex((candidate) => candidate.dagRunId === run.dagRunId) ===
      index
  );
  const dagRunsByID = new Map(detail.dagRuns.map((run) => [run.dagRunId, run]));

  return (
    <div className="mx-auto flex w-full max-w-7xl flex-col gap-5 pb-8">
      <ControllerPageHeader
        detail={detail}
        activeTab="status"
        actions={
          <>
            {canWrite && !active && (
              <Button asChild variant="outline">
                <Link to={`/controllers/${encodeURIComponent(detail.id)}/spec`}>
                  <Edit3 className="h-4 w-4" />
                  Edit
                </Link>
              </Button>
            )}
            {canExecute && active ? (
              <Button
                variant="outline"
                disabled={pending}
                onClick={() =>
                  void execute(() => api.stop(detail.id), 'Stop signal sent')
                }
              >
                <Square className="h-4 w-4" />
                Stop
              </Button>
            ) : canExecute && startable ? (
              <Button variant="primary" onClick={() => setStartOpen(true)}>
                <Play className="h-4 w-4" />
                {runtime.status === ControllerStatus.NotStarted
                  ? 'Start'
                  : 'Run again'}
              </Button>
            ) : null}
          </>
        }
      />

      {actionError && (
        <Alert variant="destructive">
          <AlertDescription>{actionError}</AlertDescription>
        </Alert>
      )}
      {runtime.lastError && (
        <Alert variant="destructive">
          <AlertTitle>Controller execution failed</AlertTitle>
          <AlertDescription>{runtime.lastError}</AlertDescription>
        </Alert>
      )}

      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <Metric label="Status">
          <ControllerStatusChip
            status={runtime.status}
            finishedAt={runtime.finishedAt}
          />
        </Metric>
        <Metric label="Current state">
          <span className="font-mono">{runtime.currentState || '—'}</span>
        </Metric>
        <Metric label="Turns">
          {runtime.turnCount} / {detail.definition.maxTurns}
        </Metric>
        <Metric label="Updated">
          <RelativeTime
            timestamp={runtime.updatedAt ?? detail.resourceUpdatedAt}
          />
        </Metric>
      </div>

      {waitingForPrompt && (
        <Card>
          <CardHeader>
            <CardTitle>Controller needs input</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            <div className="rounded-md border border-border bg-muted/30 p-3">
              <pre className="whitespace-pre-wrap break-words font-sans text-sm">
                {runtime.waitingQuestion}
              </pre>
            </div>
            <Textarea
              value={prompt}
              maxLength={MAX_CONTROLLER_PROMPT_BYTES}
              onChange={(event) => setPrompt(event.target.value)}
              placeholder="Reply to the Controller…"
              className="min-h-24"
            />
            <div className="flex justify-between text-xs text-muted-foreground">
              <span className="text-destructive">
                {prompt ? promptError : null}
              </span>
              <span>
                {promptBytes} / {MAX_CONTROLLER_PROMPT_BYTES} bytes
              </span>
            </div>
            <div className="flex items-center justify-between gap-3">
              <p className="text-xs text-muted-foreground">
                Stored in context and potentially sent to an external LLM. Do
                not include secrets.
              </p>
              <Button
                variant="primary"
                disabled={!canExecute || pending || promptError !== null}
                onClick={() =>
                  void (async () => {
                    const sent = await execute(
                      () => api.prompt(detail.id, prompt),
                      'Prompt sent'
                    );
                    if (sent) setPrompt('');
                  })()
                }
              >
                <Send className="h-4 w-4" />
                Send
              </Button>
            </div>
          </CardContent>
        </Card>
      )}

      {waitingForApproval && runtime.activeDAGRun && (
        <Alert variant="warning">
          <AlertTitle>Child DAG is waiting</AlertTitle>
          <AlertDescription>
            Continue in the{' '}
            {dagRunsByID.has(runtime.activeDAGRun.dagRunId) ? (
              <Link
                className="font-medium underline"
                to={buildDAGRunPageURL({
                  rootDAGRunName: runtime.activeDAGRun.dag,
                  rootDAGRunId: runtime.activeDAGRun.dagRunId,
                  remoteNode: 'local',
                })}
              >
                {runtime.activeDAGRun.dag} DAG run
              </Link>
            ) : (
              <span className="font-medium">
                {runtime.activeDAGRun.dag} DAG run
              </span>
            )}
            . Approval and interaction stay with that run.
          </AlertDescription>
        </Alert>
      )}

      <section className="space-y-2">
        <h3 className="text-lg font-semibold">State graph</h3>
        <ControllerGraph
          definition={detail.definition}
          currentState={runtime.currentState}
        />
      </section>

      <div className="grid gap-5 lg:grid-cols-[0.75fr_1.25fr]">
        <Card>
          <CardHeader>
            <CardTitle>Current and recent DAG runs</CardTitle>
          </CardHeader>
          <CardContent>
            {refs.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                No child DAG runs yet.
              </p>
            ) : (
              <ul className="divide-y divide-border">
                {refs.map((run) => {
                  const summary = dagRunsByID.get(run.dagRunId);
                  const isActive =
                    runtime.activeDAGRun?.dagRunId === run.dagRunId;
                  const runStatusLabel =
                    summary?.statusLabel ?? (isActive ? 'Pending' : 'Expired');
                  return (
                    <li
                      key={run.dagRunId}
                      className="flex items-center justify-between gap-3 py-3 text-sm"
                    >
                      <div>
                        {summary ? (
                          <Link
                            className="font-medium text-primary hover:underline"
                            to={buildDAGRunPageURL({
                              rootDAGRunName: run.dag,
                              rootDAGRunId: run.dagRunId,
                              remoteNode: 'local',
                            })}
                          >
                            {run.dag}
                          </Link>
                        ) : (
                          <span className="font-medium">{run.dag}</span>
                        )}
                        <div className="font-mono text-xs text-muted-foreground">
                          {run.dagRunId}
                        </div>
                      </div>
                      <div className="text-right text-xs">
                        <div>{runStatusLabel}</div>
                        <div className="font-mono text-muted-foreground">
                          {run.state}
                        </div>
                      </div>
                    </li>
                  );
                })}
              </ul>
            )}
          </CardContent>
        </Card>
        <section className="space-y-2">
          <h3 className="text-lg font-semibold">
            Router context and decisions
          </h3>
          <ControllerContext messages={runtime.context} />
        </section>
      </div>

      <ControllerPromptDialog
        open={startOpen}
        title={
          runtime.status === ControllerStatus.NotStarted
            ? 'Start Controller'
            : 'Run Controller again'
        }
        description={
          runtime.status === ControllerStatus.NotStarted
            ? 'Enter the outcome this Controller should achieve.'
            : 'The current state and Router context will be replaced. Existing DAG runs remain available.'
        }
        submitLabel={
          runtime.status === ControllerStatus.NotStarted ? 'Start' : 'Run again'
        }
        pending={pending}
        onOpenChange={setStartOpen}
        onSubmit={async (startPrompt) => {
          const started = await execute(
            () => api.start(detail.id, startPrompt),
            'Controller started'
          );
          if (started) setStartOpen(false);
        }}
      />
    </div>
  );
}
