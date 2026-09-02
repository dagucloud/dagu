// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';
import { parseParams } from '@/lib/parseParams';
import { FileCode2, GitBranch, Target } from 'lucide-react';
import React from 'react';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';

type DAG = components['schemas']['DAGDetails'];
type Step = components['schemas']['Step'];

const AGENT_SYSTEM_STEPS = new Set(['__agent__', 'ask_user']);

type Props = {
  dag: DAG;
};

/** Agent-oriented summary and inspection surface for runtime actions. */
export function AgentSpecOverview({ dag }: Props) {
  const tasks = dag.tasks ?? [];
  const steps = dag.steps ?? [];
  const actions = steps.filter(
    (step) => !AGENT_SYSTEM_STEPS.has(step.name)
  );
  const [selectedActionName, setSelectedActionName] = React.useState(
    actions[0]?.name ?? null
  );
  const selectedAction =
    actions.find((step) => step.name === selectedActionName) ?? actions[0];

  return (
    <div className="space-y-4">
      <section className="rounded-md border border-border bg-card px-5 py-4 shadow-sm">
        <h2 className="flex items-center gap-2 text-base font-semibold text-foreground">
          <Target className="h-4 w-4 text-primary" />
          <I18nText text={"Tasks"} />
        </h2>

        {tasks.length > 0 ? (
          <div className="mt-3 divide-y divide-border">
            {tasks.map((task) => (
              <div key={task.name} className="py-2.5 first:pt-1 last:pb-0">
                <div className="min-w-0">
                  <div className="whitespace-normal break-words text-sm font-medium text-foreground">
                    {task.name}
                  </div>
                  {task.description ? (
                    <div className="mt-1 whitespace-normal break-words text-sm leading-relaxed text-foreground">
                      {task.description}
                    </div>
                  ) : null}
                </div>
              </div>
            ))}
          </div>
        ) : (
          <div className="mt-3 text-sm text-foreground">
            <I18nText text={"No tasks are defined."} />
          </div>
        )}
      </section>

      <section className="overflow-hidden rounded-md border border-border bg-card shadow-sm lg:grid lg:h-[clamp(32rem,calc(100vh-22rem),48rem)] lg:grid-cols-[minmax(280px,0.75fr)_minmax(0,1.25fr)]">
        <I18nProps><div
          role="group"
          className="min-w-0 border-b border-border lg:overflow-y-auto lg:border-b-0 lg:border-r"
          aria-label="Available actions"
        >
          {actions.length > 0 ? (
            <div className="divide-y divide-border">
              {actions.map((step) => {
                const isSelected = selectedAction?.name === step.name;

                return (
                  <button
                    key={step.name}
                    type="button"
                    aria-pressed={isSelected}
                    className={cn(
                      'relative block min-h-[76px] w-full px-5 py-3 text-left outline-none transition-colors hover:bg-muted/40 focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary',
                      isSelected &&
                        'bg-primary/[0.08] before:absolute before:inset-y-0 before:left-0 before:w-0.5 before:bg-primary'
                    )}
                    onClick={() => setSelectedActionName(step.name)}
                  >
                    <div className="min-w-0">
                      <div className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5">
                        <span className="whitespace-normal break-words text-sm font-semibold text-foreground">
                          {step.name}
                        </span>
                        {step.id && step.id !== step.name ? (
                          <span className="truncate font-mono text-xs text-foreground">
                            {step.id}
                          </span>
                        ) : null}
                      </div>
                      {step.description ? (
                        <p className="mt-1 line-clamp-1 whitespace-normal break-words text-sm leading-5 text-foreground">
                          {step.description}
                        </p>
                      ) : null}
                    </div>
                  </button>
                );
              })}
            </div>
          ) : (
            <div className="px-5 py-6 text-sm text-foreground">
              <I18nText text={"No actions are defined."} />
            </div>
          )}
        </div></I18nProps>

        <ActionDetails step={selectedAction} />
      </section>
    </div>
  );
}

function ActionDetails({ step }: { step?: Step }) {
  if (!step) {
    return (
      <div className="flex min-h-64 items-center justify-center px-6 py-10 text-sm text-foreground">
        <I18nText text={"Select an action to inspect its configuration."} />
      </div>
    );
  }

  const execution = getExecutionSummary(step);
  const parameters = step.params ? parseParams(step.params) : [];
  const executorConfig = Object.entries(step.executorConfig?.config ?? {});

  return (
    <div className="min-w-0 px-5 py-5 lg:h-full lg:overflow-y-auto lg:px-6">
      <div>
        <div className="min-w-0">
          <h2 className="break-words text-lg font-semibold text-foreground">
            {step.name}
          </h2>
          {step.id ? (
            <div className="mt-1 font-mono text-sm text-foreground">
              <I18nText text={"ID:"} /> {step.id}
            </div>
          ) : null}
          {step.description ? (
            <p className="mt-3 max-w-3xl text-sm leading-relaxed text-foreground">
              {step.description}
            </p>
          ) : null}
        </div>
      </div>

      <I18nProps><DetailSection title="Execution" className="mt-5">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <Badge variant={step.call ? 'primary' : 'outline'}>
            {step.call ? <GitBranch className="h-3 w-3" /> : null}
            {execution.label}
          </Badge>
          {execution.detail ? (
            <code className="break-all text-sm text-foreground">
              {execution.detail}
            </code>
          ) : null}
        </div>

        {step.script ? <CodePreview value={step.script} /> : null}
        {step.commands?.length ? (
          <CodePreview
            value={step.commands
              .map((command) =>
                [command.command, ...(command.args ?? [])].join(' ')
              )
              .join('\n')}
          />
        ) : null}
        {executorConfig.length > 0 ? (
          <KeyValueGrid
            items={executorConfig.map(([name, value]) => ({
              name,
              value: formatValue(value),
            }))}
          />
        ) : null}
      </DetailSection></I18nProps>

      <I18nProps><DetailSection title="Parameters">
        {parameters.length > 0 ? (
          <KeyValueGrid
            items={parameters.map((parameter, index) => ({
              name: parameter.Name ?? `Argument ${index + 1}`,
              value: parameter.Value,
            }))}
          />
        ) : (
          <EmptyValue><I18nText text={"No parameters"} /></EmptyValue>
        )}
      </DetailSection></I18nProps>

      <AdditionalSettings step={step} />

      <div className="pt-5">
        <I18nProps><InfoCard title="Conditions">
          {step.preconditions?.length ? (
            <div className="space-y-2">
              {step.preconditions.map((condition, index) => (
                <div
                  key={`${condition.condition}-${index}`}
                  className="text-sm leading-relaxed text-foreground"
                >
                  <code className="break-all">{condition.condition}</code>
                  <span className="px-1.5 text-foreground">→</span>
                  <code className="break-all">{condition.expected}</code>
                </div>
              ))}
            </div>
          ) : (
            <EmptyValue><I18nText text={"None"} /></EmptyValue>
          )}
        </InfoCard></I18nProps>
      </div>
    </div>
  );
}

function DetailSection({
  title,
  className,
  children,
}: {
  title: string;
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <section className={cn('space-y-3 border-b border-border py-5', className)}>
      <h3 className="text-sm font-semibold text-foreground">{title}</h3>
      {children}
    </section>
  );
}

function KeyValueGrid({
  items,
}: {
  items: Array<{ name: string; value: string }>;
}) {
  return (
    <dl className="overflow-hidden rounded-md border border-border">
      {items.map((item, index) => (
        <div
          key={`${item.name}-${index}`}
          className="grid min-w-0 border-b border-border last:border-b-0 sm:grid-cols-[minmax(120px,0.55fr)_minmax(0,1.45fr)]"
        >
          <dt className="bg-muted/20 px-3 py-2 text-sm font-medium text-foreground sm:border-r sm:border-border">
            {item.name}
          </dt>
          <dd className="min-w-0 px-3 py-2 font-mono text-sm text-foreground">
            <span className="whitespace-pre-wrap break-all">{item.value}</span>
          </dd>
        </div>
      ))}
    </dl>
  );
}

function CodePreview({ value }: { value: string }) {
  return (
    <div className="flex min-w-0 gap-2 rounded-md border border-border bg-muted/20 px-3 py-2.5">
      <FileCode2 className="mt-0.5 h-4 w-4 shrink-0 text-primary" />
      <pre className="max-h-40 min-w-0 overflow-auto whitespace-pre-wrap break-words font-mono text-[13px] leading-6 text-foreground">
        {value}
      </pre>
    </div>
  );
}

function AdditionalSettings({ step }: { step: Step }) {
  const settings = [
    step.dir ? { name: 'Working directory', value: step.dir } : null,
    step.output ? { name: 'Output variable', value: step.output } : null,
    step.stdout ? { name: 'stdout', value: step.stdout } : null,
    step.stderr ? { name: 'stderr', value: step.stderr } : null,
    step.timeoutSec !== undefined
      ? { name: 'Timeout', value: `${step.timeoutSec}s` }
      : null,
    step.repeatPolicy
      ? { name: 'Repeat policy', value: formatValue(step.repeatPolicy) }
      : null,
    step.parallel
      ? { name: 'Parallel', value: formatValue(step.parallel) }
      : null,
    step.outputs?.length
      ? { name: 'Outputs', value: formatValue(step.outputs) }
      : null,
  ].filter((setting): setting is { name: string; value: string } => !!setting);

  if (settings.length === 0) {
    return null;
  }

  return (
    <I18nProps><DetailSection title="Configuration">
      <KeyValueGrid items={settings} />
    </DetailSection></I18nProps>
  );
}

function InfoCard({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <section className="min-w-0 rounded-md border border-border bg-muted/10 p-4">
      <h3 className="mb-3 text-sm font-semibold text-foreground">{title}</h3>
      {children}
    </section>
  );
}

function EmptyValue({ children }: { children: React.ReactNode }) {
  return <div className="text-sm text-foreground">{children}</div>;
}

function getExecutionSummary(step: Step): {
  label: string;
  detail?: string;
} {
  if (step.call) {
    return { label: 'Sub-DAG', detail: step.call };
  }
  if (step.executorConfig?.type) {
    return { label: formatLabel(step.executorConfig.type) };
  }
  if (step.script) {
    return { label: 'Script' };
  }
  if (step.commands?.length) {
    return {
      label: step.commands.length === 1 ? 'Command' : 'Commands',
      detail: `${step.commands.length}`,
    };
  }
  if (step.humanTask) {
    return { label: 'Human task' };
  }
  return { label: 'Step' };
}

function formatLabel(value: string): string {
  const label = value.replace(/[-_]+/g, ' ');
  return label.charAt(0).toUpperCase() + label.slice(1);
}

function formatValue(value: unknown): string {
  if (typeof value === 'string') {
    return value;
  }
  if (typeof value === 'number' || typeof value === 'boolean') {
    return String(value);
  }
  return JSON.stringify(value);
}
