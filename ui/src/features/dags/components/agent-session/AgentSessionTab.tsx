// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Button } from '@/components/ui/button';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { Tab, Tabs } from '@/components/ui/tabs';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useClient } from '@/hooks/api';
import {
  Bot,
  Check,
  CircleDollarSign,
  FileDiff,
  RotateCcw,
  ShieldAlert,
  Wrench,
  X,
} from 'lucide-react';
import React from 'react';
import { combineQuestionAnswer } from './agentSessionAnswers';

import {
  AgentInteractionResponseRequestDecision,
  components,
  NodeStatus,
} from '../../../../api/v1/schema';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';

type DAGRunDetails = components['schemas']['DAGRunDetails'];
type AgentInteraction = components['schemas']['AgentInteraction'];
type AgentSessionEvent = components['schemas']['AgentSessionEvent'];
type NodeData = components['schemas']['Node'];

type Props = {
  dagRun: DAGRunDetails;
  onChanged: () => void | Promise<void>;
  selectedStep: string;
  onSelectedStepChange: (stepName: string) => void;
};

function stateClasses(state: string) {
  switch (state) {
    case 'waiting':
      return 'border-warning/40 bg-warning/10 text-warning';
    case 'failed':
    case 'unavailable':
      return 'border-destructive/40 bg-destructive/10 text-destructive';
    case 'succeeded':
      return 'border-success/40 bg-success/10 text-success';
    default:
      return 'border-border bg-muted text-muted-foreground';
  }
}

function EventIcon({ event }: { event: AgentSessionEvent }) {
  if (event.type === 'tool') return <Wrench className="h-4 w-4" />;
  if (event.type === 'patch') return <FileDiff className="h-4 w-4" />;
  if (event.type === 'interaction') return <ShieldAlert className="h-4 w-4" />;
  return <Bot className="h-4 w-4" />;
}

function AgentTimeline({ events }: { events: AgentSessionEvent[] }) {
  if (events.length === 0) {
    return (
      <div className="rounded-md border border-dashed border-border p-5 text-center text-sm text-muted-foreground">
        <I18nText text={"OpenCode has not emitted any timeline events yet."} />
      </div>
    );
  }

  return (
    <div className="space-y-2">
      {events.map((event) => (
        <div
          key={`${event.sequence}-${event.id}`}
          className="flex gap-3 rounded-md border border-border bg-background p-3"
        >
          <div className="mt-0.5 text-muted-foreground">
            <EventIcon event={event} />
          </div>
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
              <span className="font-medium text-foreground">
                {event.name || event.type}
              </span>
              {event.status && <span>{event.status}</span>}
              {event.role && <span>{event.role}</span>}
              {event.timestamp && (
                <time>{new Date(event.timestamp).toLocaleString()}</time>
              )}
            </div>
            {event.content && (
              <div className="mt-1 whitespace-pre-wrap break-words text-sm">
                {event.content}
              </div>
            )}
            {event.files && event.files.length > 0 && (
              <div className="mt-2 space-y-1 font-mono text-xs text-muted-foreground">
                {event.files.map((file) => (
                  <div key={file} className="truncate" title={file}>
                    {file}
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>
      ))}
    </div>
  );
}

function InteractionCard({
  interaction,
  busy,
  onPermission,
  onQuestion,
}: {
  interaction: AgentInteraction;
  busy: boolean;
  onPermission: (
    decision: AgentInteractionResponseRequestDecision
  ) => Promise<void>;
  onQuestion: (answers?: string[][]) => Promise<void>;
}) {
  const [answers, setAnswers] = React.useState<Record<number, string[]>>({});
  const [customAnswers, setCustomAnswers] = React.useState<
    Record<number, string>
  >({});

  if (interaction.kind === 'permission') {
    return (
      <div className="space-y-3 rounded-lg border border-warning/40 bg-warning/5 p-4">
        <div>
          <div className="flex items-center gap-2 font-medium">
            <ShieldAlert className="h-4 w-4 text-warning" />
            <I18nText text={"Permission requested"} />
          </div>
          <div className="mt-1 text-sm">{interaction.permission}</div>
          {interaction.patterns && interaction.patterns.length > 0 && (
            <div className="mt-2 space-y-1 font-mono text-xs text-muted-foreground">
              {interaction.patterns.map((pattern) => (
                <div key={pattern}>{pattern}</div>
              ))}
            </div>
          )}
          {!!interaction.allowForSessionPatterns?.length && (
            <div className="mt-2 text-xs text-muted-foreground">
              <I18nText text={"Session scope:"} />{' '}
              <span className="font-mono">
                {interaction.allowForSessionPatterns.join(', ')}
              </span>
            </div>
          )}
        </div>
        <div className="flex flex-wrap gap-2">
          <Button
            size="sm"
            disabled={busy}
            onClick={() =>
              onPermission(AgentInteractionResponseRequestDecision.once)
            }
          >
            <Check className="h-4 w-4" /> <I18nText text={"Allow once"} />
          </Button>
          {!!interaction.allowForSessionPatterns?.length && (
            <Button
              size="sm"
              variant="secondary"
              disabled={busy}
              title={`Scope: ${interaction.allowForSessionPatterns.join(', ')}`}
              onClick={() =>
                onPermission(AgentInteractionResponseRequestDecision.session)
              }
            >
              <I18nText text={"Allow for this Dagu session"} />
            </Button>
          )}
          <Button
            size="sm"
            variant="destructive"
            disabled={busy}
            onClick={() =>
              onPermission(AgentInteractionResponseRequestDecision.reject)
            }
          >
            <X className="h-4 w-4" /> <I18nText text={"Reject"} />
          </Button>
        </div>
      </div>
    );
  }

  const questions = interaction.questions || [];
  const questionAnswers = (index: number) =>
    combineQuestionAnswer(
      answers[index] || [],
      customAnswers[index] || '',
      !!questions[index]?.multiple
    );
  const complete = questions.every(
    (_, index) => questionAnswers(index).length > 0
  );
  const toggleOption = (
    questionIndex: number,
    option: string,
    multiple: boolean
  ) => {
    setAnswers((current) => {
      const selected = current[questionIndex] || [];
      if (!multiple) return { ...current, [questionIndex]: [option] };
      return {
        ...current,
        [questionIndex]: selected.includes(option)
          ? selected.filter((item) => item !== option)
          : [...selected, option],
      };
    });
  };

  return (
    <div className="space-y-4 rounded-lg border border-warning/40 bg-warning/5 p-4">
      <div className="flex items-center gap-2 font-medium">
        <Bot className="h-4 w-4 text-warning" /> <I18nText text={"OpenCode needs an answer"} />
      </div>
      {questions.map((question, questionIndex) => (
        <div key={`${interaction.id}-${questionIndex}`} className="space-y-2">
          <div>
            <div className="text-xs font-medium uppercase text-muted-foreground">
              {question.header}
            </div>
            <div className="text-sm">{question.question}</div>
          </div>
          <div className="flex flex-wrap gap-2">
            {(question.options || []).map((option) => {
              const selected = (answers[questionIndex] || []).includes(
                option.label
              );
              return (
                <button
                  type="button"
                  key={option.label}
                  disabled={busy}
                  title={option.description}
                  onClick={() =>
                    toggleOption(
                      questionIndex,
                      option.label,
                      !!question.multiple
                    )
                  }
                  className={`rounded-md border px-3 py-2 text-left text-sm transition-colors ${
                    selected
                      ? 'border-primary bg-primary/10 text-primary'
                      : 'border-border bg-background hover:bg-muted'
                  }`}
                >
                  <div className="font-medium">{option.label}</div>
                  {option.description && (
                    <div className="mt-0.5 text-xs text-muted-foreground">
                      {option.description}
                    </div>
                  )}
                </button>
              );
            })}
          </div>
          {question.custom && (
            <I18nProps><input
              type="text"
              disabled={busy}
              placeholder="Type a custom answer"
              className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm"
              value={customAnswers[questionIndex] || ''}
              onChange={(event) =>
                setCustomAnswers((current) => ({
                  ...current,
                  [questionIndex]: event.target.value,
                }))
              }
            /></I18nProps>
          )}
        </div>
      ))}
      <div className="flex flex-wrap gap-2">
        <Button
          size="sm"
          disabled={busy || !complete}
          onClick={() =>
            onQuestion(questions.map((_, index) => questionAnswers(index)))
          }
        >
          <Check className="h-4 w-4" /> <I18nText text={"Submit answer"} />
        </Button>
        <Button
          size="sm"
          variant="destructive"
          disabled={busy}
          onClick={() => onQuestion()}
        >
          <X className="h-4 w-4" /> <I18nText text={"Reject"} />
        </Button>
      </div>
    </div>
  );
}

function AgentSessionCard({
  node,
  dagRun,
  onChanged,
}: {
  node: NodeData;
  dagRun: DAGRunDetails;
  onChanged: Props['onChanged'];
}) {
  const client = useClient();
  const remoteNode = useRemoteNode();
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState<string>();
  const [confirmRestart, setConfirmRestart] = React.useState(false);
  const session = node.agentSession!;
  const pending = (session.interactions || []).filter(
    (interaction) => interaction.status === 'pending'
  );
  const isSubRun = !!(
    dagRun.rootDAGRunId &&
    dagRun.rootDAGRunName &&
    dagRun.rootDAGRunId !== dagRun.dagRunId
  );
  const path = {
    name: isSubRun ? dagRun.rootDAGRunName! : dagRun.name,
    dagRunId: isSubRun ? dagRun.rootDAGRunId! : dagRun.dagRunId,
    stepName: node.step.name,
    ...(isSubRun ? { subDAGRunId: dagRun.dagRunId } : {}),
  };

  const runAction = async (
    action: () => Promise<{ error?: { message?: string } }>
  ) => {
    setBusy(true);
    setError(undefined);
    try {
      const result = await action();
      if (result.error)
        throw new Error(result.error.message || 'Agent action failed');
      await onChanged();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Agent action failed');
      await Promise.resolve(onChanged()).catch(() => undefined);
    } finally {
      setBusy(false);
    }
  };

  const respond = (
    interaction: AgentInteraction,
    body: {
      decision?: AgentInteractionResponseRequestDecision;
      answers?: string[][];
    }
  ) =>
    runAction(async () => {
      const endpoint = isSubRun
        ? '/dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}/steps/{stepName}/agent-interactions/{interactionId}/respond'
        : '/dag-runs/{name}/{dagRunId}/steps/{stepName}/agent-interactions/{interactionId}/respond';
      return client.POST(endpoint, {
        params: {
          path: { ...path, interactionId: interaction.id },
          query: { remoteNode },
        },
        body,
      });
    });

  const restart = () =>
    runAction(async () => {
      const endpoint = isSubRun
        ? '/dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}/steps/{stepName}/agent-session/restart'
        : '/dag-runs/{name}/{dagRunId}/steps/{stepName}/agent-session/restart';
      const result = await client.POST(endpoint, {
        params: { path, query: { remoteNode } },
      });
      setConfirmRestart(false);
      return result;
    });

  const terminalNodeStatuses = [
    NodeStatus.Failed,
    NodeStatus.Aborted,
    NodeStatus.Success,
    NodeStatus.Skipped,
    NodeStatus.PartialSuccess,
    NodeStatus.Rejected,
  ];
  const canRestart =
    (node.status === NodeStatus.Waiting &&
      (session.state === 'waiting' || session.state === 'unavailable')) ||
    terminalNodeStatuses.includes(node.status);

  return (
    <section className="space-y-4 rounded-lg border border-border bg-surface p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="font-semibold">{node.step.name}</h3>
            <span
              className={`rounded-full border px-2 py-0.5 text-xs font-medium ${stateClasses(
                session.state
              )}`}
            >
              {session.state}
            </span>
            {session.generation && session.generation > 1 && (
              <span className="text-xs text-muted-foreground">
                <I18nText text={"Session"} /> {session.generation}
              </span>
            )}
          </div>
          <div className="mt-1 text-xs text-muted-foreground">
            {[session.agent, session.model, session.variant]
              .filter(Boolean)
              .join(' · ') || <I18nText text={"OpenCode managed session"} />}
            {session.providerVersion &&
              ` · OpenCode ${session.providerVersion}`}
          </div>
        </div>
        {canRestart && (
          <Button
            size="sm"
            variant={session.state === 'unavailable' ? 'primary' : 'secondary'}
            disabled={busy}
            onClick={() => setConfirmRestart(true)}
          >
            <RotateCcw className="h-4 w-4" /> <I18nText text={"Start clean session"} />
          </Button>
        )}
      </div>

      {session.lastError &&
        (session.state === 'failed' || session.state === 'unavailable') && (
          <div className="rounded-md border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">
            {session.lastError}
          </div>
        )}
      {error && (
        <div
          role="alert"
          className="rounded-md border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive"
        >
          {error}
        </div>
      )}

      {pending.map((interaction) => (
        <InteractionCard
          key={interaction.id}
          interaction={interaction}
          busy={busy}
          onPermission={(decision) => respond(interaction, { decision })}
          onQuestion={(answers) =>
            respond(
              interaction,
              answers
                ? { answers }
                : { decision: AgentInteractionResponseRequestDecision.reject }
            )
          }
        />
      ))}

      {session.usage &&
        ((session.usage.totalTokens || 0) > 0 ||
          (session.usage.cost || 0) > 0) && (
          <div className="flex flex-wrap items-center gap-3 text-xs text-muted-foreground">
            <CircleDollarSign className="h-4 w-4" />
            <span>
              {(session.usage.totalTokens || 0).toLocaleString()} <I18nText text={"tokens"} />
            </span>
            {(session.usage.cost || 0) > 0 && (
              <span>${session.usage.cost!.toFixed(4)}</span>
            )}
          </div>
        )}

      <div>
        <h4 className="mb-2 text-sm font-medium"><I18nText text={"Session timeline"} /></h4>
        <AgentTimeline events={session.events || []} />
      </div>

      <I18nProps><ConfirmDialog
        title="Start a clean OpenCode session?"
        buttonText="Start clean session"
        visible={confirmRestart}
        dismissModal={() => setConfirmRestart(false)}
        onSubmit={restart}
        submitDisabled={busy}
      >
        <I18nText text={"This starts a new conversation and retries this step with its original prompt. The previous conversation is retained until this DAG run is deleted. Files already changed in the workspace are not reverted."} />
      </ConfirmDialog></I18nProps>
    </section>
  );
}

function preferredAgentNode(nodes: NodeData[]) {
  return (
    nodes.find(
      (node) =>
        node.agentSession?.state === 'waiting' ||
        node.agentSession?.state === 'unavailable'
    ) ||
    nodes.find(
      (node) =>
        node.agentSession?.state === 'running' ||
        node.agentSession?.state === 'starting'
    ) ||
    nodes[nodes.length - 1]
  );
}

export function AgentSessionTab({
  dagRun,
  onChanged,
  selectedStep,
  onSelectedStepChange,
}: Props) {
  const nodes = (dagRun.nodes || []).filter((node) => !!node.agentSession);
  const tabGroupId = React.useId();
  const panelId = `${tabGroupId}-panel`;
  const selectedNode =
    nodes.find((node) => node.step.name === selectedStep) ||
    preferredAgentNode(nodes);
  const resolvedStep = selectedNode?.step.name || '';

  React.useEffect(() => {
    if (resolvedStep !== selectedStep) {
      onSelectedStepChange(resolvedStep);
    }
  }, [onSelectedStepChange, resolvedStep, selectedStep]);

  if (!selectedNode) {
    return (
      <div className="py-8 text-center text-sm text-muted-foreground">
        <I18nText text={"No managed agent sessions in this run."} />
      </div>
    );
  }

  const selectedIndex = nodes.findIndex(
    (node) => node.step.name === selectedNode.step.name
  );
  const tabId = (index: number) => `${tabGroupId}-${index}-tab`;
  const selectNode = (index: number) => {
    const node = nodes[index];
    if (node) onSelectedStepChange(node.step.name);
  };
  const handleTabKeyDown = (
    event: React.KeyboardEvent<HTMLElement>,
    index: number
  ) => {
    let nextIndex: number;
    switch (event.key) {
      case 'ArrowLeft':
        nextIndex = (index - 1 + nodes.length) % nodes.length;
        break;
      case 'ArrowRight':
        nextIndex = (index + 1) % nodes.length;
        break;
      case 'Home':
        nextIndex = 0;
        break;
      case 'End':
        nextIndex = nodes.length - 1;
        break;
      default:
        return;
    }
    event.preventDefault();
    selectNode(nextIndex);
    document.getElementById(tabId(nextIndex))?.focus();
  };

  return (
    <div className="space-y-4">
      {nodes.length > 1 && (
        <I18nProps><Tabs
          role="tablist"
          aria-label="Agent conversations"
          className="flex w-full overflow-x-auto overflow-y-hidden"
        >
          {nodes.map((node, index) => {
            const selected = node.step.name === selectedNode.step.name;
            return (
              <Tab
                key={node.step.name}
                id={tabId(index)}
                role="tab"
                isActive={selected}
                aria-selected={selected}
                aria-controls={panelId}
                tabIndex={selected ? 0 : -1}
                onClick={() => selectNode(index)}
                onKeyDown={(event) => handleTabKeyDown(event, index)}
                className="h-9 shrink-0 gap-2 px-3"
              >
                <span>{node.step.name}</span>
                <span
                  className={`rounded-full border px-1.5 py-0.5 text-[11px] ${stateClasses(
                    node.agentSession!.state
                  )}`}
                >
                  {node.agentSession!.state}
                </span>
              </Tab>
            );
          })}
        </Tabs></I18nProps>
      )}
      <div
        {...(nodes.length > 1
          ? {
              id: panelId,
              role: 'tabpanel',
              'aria-labelledby': tabId(selectedIndex),
            }
          : {})}
      >
        <AgentSessionCard
          key={selectedNode.step.name}
          node={selectedNode}
          dagRun={dagRun}
          onChanged={onChanged}
        />
      </div>
    </div>
  );
}
