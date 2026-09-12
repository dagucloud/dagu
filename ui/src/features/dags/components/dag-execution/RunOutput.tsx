// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React, { useEffect, useId, useState } from 'react';
import {
  Check,
  Circle,
  CircleAlert,
  LoaderCircle,
  Pin,
  Terminal,
} from 'lucide-react';
import { components, NodeStatus, Status, Stream } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useI18n } from '@/i18n/I18nProvider';
import { isActiveNodeStatus } from '@/lib/status-utils';
import { cn } from '@/lib/utils';
import StepLog from './StepLog';

type Node = components['schemas']['Node'];
type Props = {
  dagRun: components['schemas']['DAGRunDetails'];
  onInspect: (node: Node) => void;
};

function isFailed(node: Node): boolean {
  return (
    node.status === NodeStatus.Failed || node.status === NodeStatus.Rejected
  );
}

function initialNode(nodes: Node[]): Node | undefined {
  return (
    nodes.find(isFailed) ||
    nodes.find((node) => isActiveNodeStatus(node.status)) ||
    nodes.find(
      (node) =>
        node.status !== NodeStatus.NotStarted &&
        node.status !== NodeStatus.Skipped
    )
  );
}

export default function RunOutput(props: Props) {
  const remoteNode = useRemoteNode();
  const identity = JSON.stringify([
    remoteNode,
    props.dagRun.name,
    props.dagRun.dagRunId,
  ]);
  return <RunOutputPanel key={identity} {...props} />;
}

function RunOutputPanel({ dagRun, onInspect }: Props) {
  const { ts } = useI18n();
  const id = useId();
  const nodes = dagRun.nodes || [];
  const [selectedName, setSelectedName] = useState(
    () => initialNode(nodes)?.step.name
  );
  const [stream, setStream] = useState(Stream.stdout);
  const [automatic, setAutomatic] = useState(true);
  const [following, setFollowing] = useState(true);
  const [settledStep, setSettledStep] = useState<string>();
  const selected = nodes.find((node) => node.step.name === selectedName);
  const running = nodes.filter((node) => isActiveNodeStatus(node.status));
  const failures = nodes.filter(isFailed);

  useEffect(() => {
    if (!selected) {
      setSelectedName(initialNode(nodes)?.step.name);
      return;
    }
    if (isActiveNodeStatus(selected.status)) {
      setSettledStep(undefined);
      return;
    }
    if (selected.status !== NodeStatus.Success) {
      setAutomatic(false);
      return;
    }
    // Parallel updates never displace an active or manually inspected step.
    if (automatic && following && settledStep === selected.step.name) {
      const next =
        nodes.find((node) => isActiveNodeStatus(node.status)) ||
        nodes.find(isFailed);
      if (next && next.step.name !== selected.step.name) {
        setSelectedName(next.step.name);
        setStream(Stream.stdout);
        setSettledStep(undefined);
      }
    }
  }, [nodes, selected, automatic, following, settledStep]);

  function selectStep(name: string): void {
    setSelectedName(name);
    setAutomatic(false);
    setFollowing(true);
    setSettledStep(undefined);
  }

  function returnToLive(): void {
    const next =
      selected && isActiveNodeStatus(selected.status)
        ? selected
        : running[0] || initialNode(nodes);
    setSelectedName(next?.step.name);
    setStream(Stream.stdout);
    setAutomatic(true);
    setFollowing(true);
    setSettledStep(undefined);
  }

  function navigateSteps(
    event: React.KeyboardEvent<HTMLButtonElement>,
    index: number
  ): void {
    const offsets: Record<string, number> = {
      ArrowDown: 1,
      ArrowUp: -1,
      Home: -index,
      End: nodes.length - 1 - index,
    };
    const offset = offsets[event.key];
    if (offset === undefined) {
      return;
    }
    event.preventDefault();
    const nextIndex = (index + offset + nodes.length) % nodes.length;
    const next = nodes[nextIndex];
    if (next) {
      selectStep(next.step.name);
      const tabs =
        event.currentTarget.parentElement?.querySelectorAll<HTMLButtonElement>(
          '[role="tab"]'
        );
      tabs?.[nextIndex]?.focus();
    }
  }

  return (
    <section
      aria-label={ts('Run output')}
      className="@container rounded-lg border border-border bg-card"
    >
      <div className="sticky top-0 z-20 flex flex-wrap items-center justify-between gap-2 rounded-t-lg border-b border-border bg-card px-3 py-2">
        <div className="flex min-w-0 items-center gap-2 text-sm">
          <Terminal
            className="h-4 w-4 shrink-0 text-muted-foreground"
            aria-hidden="true"
          />
          <span className="font-medium">{ts('Run output')}</span>
          <span
            role="status"
            className="flex items-center gap-2 text-xs text-muted-foreground"
          >
            {running.length > 0 &&
              ts('{count} running', { count: running.length })}
            {failures.length > 0 && (
              <button
                type="button"
                className="rounded text-destructive underline underline-offset-2 focus-visible:outline-ring"
                onClick={() => selectStep(failures[0]!.step.name)}
              >
                {ts('{count} failed', { count: failures.length })}
              </button>
            )}
          </span>
        </div>
        <Button
          size="sm"
          variant="ghost"
          onClick={returnToLive}
          disabled={following && (automatic || running.length === 0)}
        >
          {!automatic && <Pin className="h-3 w-3" aria-hidden="true" />}
          {ts('Back to live')}
        </Button>
      </div>

      <div className="grid min-w-0 grid-cols-1 @3xl:grid-cols-[14rem_minmax(0,1fr)]">
        <div
          role="tablist"
          aria-label={ts('Steps')}
          aria-orientation="vertical"
          className="hidden max-h-[38rem] overflow-auto border-r border-border p-2 @3xl:block"
        >
          {nodes.map((node, index) => {
            const active = isActiveNodeStatus(node.status);
            const failed = isFailed(node);
            const Icon = active
              ? LoaderCircle
              : failed
                ? CircleAlert
                : node.status === NodeStatus.Success
                  ? Check
                  : Circle;
            return (
              <button
                key={node.step.name}
                id={`${id}-step-${index}`}
                role="tab"
                aria-selected={node.step.name === selectedName}
                aria-controls={`${id}-log`}
                tabIndex={
                  node.step.name === selectedName || (!selected && index === 0)
                    ? 0
                    : -1
                }
                onClick={() => selectStep(node.step.name)}
                onKeyDown={(event) => navigateSteps(event, index)}
                className={cn(
                  'flex w-full items-start gap-2 rounded-md border border-transparent px-2 py-2 text-left text-sm outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring',
                  node.step.name === selectedName
                    ? 'border-border bg-muted'
                    : 'hover:bg-muted/60'
                )}
              >
                <Icon
                  aria-hidden="true"
                  className={cn(
                    'mt-0.5 h-4 w-4 shrink-0',
                    active && 'text-primary motion-safe:animate-spin',
                    failed && 'text-destructive',
                    node.status === NodeStatus.Success && 'text-success'
                  )}
                />
                <span className="min-w-0">
                  <span className="block break-words font-medium">
                    {node.step.name}
                  </span>
                  <span
                    className={cn(
                      'block text-xs text-muted-foreground',
                      failed && 'text-destructive'
                    )}
                  >
                    {ts(node.statusLabel)}
                  </span>
                </span>
              </button>
            );
          })}
        </div>

        <div className="min-w-0 p-3">
          <div className="mb-3 text-xs text-muted-foreground @3xl:hidden">
            <label htmlFor={`${id}-select`}>{ts('Step')}</label>
            <select
              id={`${id}-select`}
              className="mt-1 h-9 w-full rounded-md border border-border bg-background px-2 text-sm text-foreground"
              value={selectedName || ''}
              onChange={(event) => selectStep(event.target.value)}
            >
              {!selected && (
                <option value="">{ts('Waiting for execution...')}</option>
              )}
              {nodes.map((node) => (
                <option key={node.step.name} value={node.step.name}>
                  {node.step.name} · {ts(node.statusLabel)}
                </option>
              ))}
            </select>
          </div>

          <div className="mb-2 flex min-h-8 flex-wrap items-center justify-between gap-2">
            <div className="min-w-0 text-sm">
              <span className="break-words font-medium">
                {selectedName || ts('Step output')}
              </span>
              {selected && (
                <span className="ml-2 text-xs text-muted-foreground">
                  {ts(selected.statusLabel)}
                </span>
              )}
            </div>
            <div className="flex items-center gap-1">
              {[Stream.stdout, Stream.stderr].map((value) => (
                <Button
                  key={value}
                  size="xs"
                  variant={stream === value ? 'secondary' : 'ghost'}
                  aria-pressed={stream === value}
                  onClick={() => {
                    setStream(value);
                    setAutomatic(false);
                    setFollowing(true);
                  }}
                >
                  {value}
                </Button>
              ))}
              {selected && (
                <Button
                  size="xs"
                  variant="ghost"
                  onClick={() => {
                    setAutomatic(false);
                    setFollowing(false);
                    onInspect(selected);
                  }}
                >
                  {ts('Inspect step')}
                </Button>
              )}
            </div>
          </div>

          {selected &&
            isFailed(selected) &&
            (selected.error || selected.rejectionReason) && (
              <p className="mb-2 max-h-20 overflow-auto whitespace-pre-wrap break-words rounded-md bg-destructive/5 p-2 text-sm text-destructive">
                {selected.error || selected.rejectionReason}
              </p>
            )}
          <div
            id={`${id}-log`}
            role="tabpanel"
            aria-label={ts('Output for {step}', { step: selectedName || '' })}
            className="h-[clamp(24rem,55vh,42rem)] min-w-0"
          >
            {selected && selected.status !== NodeStatus.NotStarted ? (
              <StepLog
                dagName={dagRun.name}
                dagRunId={dagRun.dagRunId}
                stepName={selected.step.name}
                dagRun={dagRun}
                node={selected}
                stream={stream}
                followTail={following}
                onFollowTailChange={setFollowing}
                onSettled={setSettledStep}
              />
            ) : (
              <div
                role="status"
                className="flex h-full items-center justify-center rounded-md bg-muted/40 text-sm text-muted-foreground"
              >
                {dagRun.status === Status.Queued ||
                dagRun.status === Status.NotStarted ||
                selected?.status === NodeStatus.NotStarted
                  ? ts('Waiting for execution...')
                  : ts('No output recorded.')}
              </div>
            )}
          </div>
        </div>
      </div>
    </section>
  );
}
