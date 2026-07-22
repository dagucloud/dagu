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
import { useSimpleToast } from '@/components/ui/simple-toast';
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
import {
  ControllerStatus,
  canStartController,
} from '@/features/controllers/types';
import { workspaceSelectionKey } from '@/lib/workspace';

function dagRunURL(dag: string, dagRunId: string): string {
  return `/dag-runs/${encodeURIComponent(dag)}/${encodeURIComponent(dagRunId)}?remoteNode=local`;
}

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
  const { showToast } = useSimpleToast();
  const workspaceKey = workspaceSelectionKey(appBar.workspaceSelection);
  const {
    data: detail,
    error,
    isLoading,
    mutate,
  } = useControllerDetail(id, workspaceKey);
  const [startOpen, setStartOpen] = React.useState(false);
  const [prompt, setPrompt] = React.useState('');
  const [pending, setPending] = React.useState(false);
  const [actionError, setActionError] = React.useState<string | null>(null);
  const promptBytes = new TextEncoder().encode(prompt).length;
  const promptTooLarge = promptBytes > 16_384;
  const canExecute = useCanExecuteForWorkspace(
    detail?.definition.labels
      .find((label) => label.startsWith('workspace='))
      ?.slice(10) ?? ''
  );
  const canWrite = useCanWriteForWorkspace(
    detail?.definition.labels
      .find((label) => label.startsWith('workspace='))
      ?.slice(10) ?? ''
  );

  React.useEffect(() => {
    if (detail) appBar.setTitle(detail.definition.name);
  }, [appBar, detail]);

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
  const active =
    runtime.status === ControllerStatus.Running ||
    runtime.status === ControllerStatus.Waiting ||
    (runtime.status === ControllerStatus.Aborted && !runtime.finishedAt);
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

  const execute = async (action: () => Promise<void>, success: string) => {
    setPending(true);
    setActionError(null);
    try {
      await action();
    } catch (failure) {
      setActionError(
        failure instanceof Error ? failure.message : 'Controller action failed'
      );
      setPending(false);
      return false;
    }
    showToast(success);
    try {
      await mutate();
    } catch {
      setActionError(
        'The action succeeded, but the latest Controller status could not be loaded.'
      );
    }
    setPending(false);
    return true;
  };

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
          <AlertTitle>{runtime.lastError.code}</AlertTitle>
          <AlertDescription>
            {runtime.lastError.message ?? 'Controller execution failed.'}
          </AlertDescription>
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
              maxLength={16_384}
              onChange={(event) => setPrompt(event.target.value)}
              placeholder="Reply to the Controller…"
              className="min-h-24"
            />
            <div className="flex justify-between text-xs text-muted-foreground">
              <span className="text-destructive">
                {promptTooLarge ? 'The prompt must be 16 KiB or less.' : ''}
              </span>
              <span>{promptBytes} / 16,384 bytes</span>
            </div>
            <div className="flex items-center justify-between gap-3">
              <p className="text-xs text-muted-foreground">
                Stored in context and potentially sent to an external LLM. Do
                not include secrets.
              </p>
              <Button
                variant="primary"
                disabled={
                  !canExecute || pending || !prompt.trim() || promptTooLarge
                }
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
            <Link
              className="font-medium underline"
              to={dagRunURL(
                runtime.activeDAGRun.dag,
                runtime.activeDAGRun.dagRunId
              )}
            >
              {runtime.activeDAGRun.dag} DAG run
            </Link>
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
                  const expired = !summary && !isActive;
                  return (
                    <li
                      key={run.dagRunId}
                      className="flex items-center justify-between gap-3 py-3 text-sm"
                    >
                      <div>
                        {expired ? (
                          <span className="font-medium">{run.dag}</span>
                        ) : (
                          <Link
                            className="font-medium text-primary hover:underline"
                            to={dagRunURL(run.dag, run.dagRunId)}
                          >
                            {run.dag}
                          </Link>
                        )}
                        <div className="font-mono text-xs text-muted-foreground">
                          {run.dagRunId}
                        </div>
                      </div>
                      <div className="text-right text-xs">
                        <div>
                          {expired
                            ? 'Expired'
                            : (summary?.statusLabel ?? 'Pending')}
                        </div>
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
