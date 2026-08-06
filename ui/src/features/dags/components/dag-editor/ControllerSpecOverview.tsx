// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';
import { Badge } from '@/components/ui/badge';
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import { Bot, ChevronRight, CircleHelp, GitBranch, Target } from 'lucide-react';
import DAGAttributes from './DAGAttributes';

type DAG = components['schemas']['DAGDetails'];
type Step = components['schemas']['Step'];

const CONTROLLER_SYSTEM_STEPS = new Set(['__controller__', 'ask_user']);

type Props = {
  dag: DAG;
  onSelectStep: (step: Step) => void;
};

/** Controller-oriented summary of goals and actions available at runtime. */
export function ControllerSpecOverview({ dag, onSelectStep }: Props) {
  const tasks = dag.tasks ?? [];
  const steps = dag.steps ?? [];
  const actions = steps.filter(
    (step) => !CONTROLLER_SYSTEM_STEPS.has(step.name)
  );
  const canAskUser = steps.some((step) => step.name === 'ask_user');

  return (
    <div className="space-y-6 pb-8">
      <div className="grid items-stretch gap-4 lg:grid-cols-[minmax(260px,0.75fr)_minmax(0,1.25fr)]">
        <Card className="gap-0 overflow-hidden">
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Bot className="h-4 w-4 text-primary" />
              Controller workflow
            </CardTitle>
            <CardDescription>
              Chooses and may repeat available actions until every goal is
              resolved.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="grid grid-cols-2 gap-3">
              <div className="rounded-md border border-border bg-muted/30 p-3">
                <div className="text-lg font-semibold text-foreground">
                  {actions.length}
                </div>
                <div className="text-xs text-muted-foreground">
                  Available actions
                </div>
              </div>
              <div className="rounded-md border border-border bg-muted/30 p-3">
                <div className="text-lg font-semibold text-foreground">
                  {tasks.length}
                </div>
                <div className="text-xs text-muted-foreground">Goals</div>
              </div>
            </div>
            <div className="flex flex-wrap gap-2">
              <Badge variant="primary">Runtime-selected order</Badge>
              {canAskUser ? (
                <Badge variant="outline">
                  <CircleHelp className="h-3 w-3" />
                  Can ask user
                </Badge>
              ) : null}
            </div>
          </CardContent>
        </Card>

        <Card className="gap-0 overflow-hidden">
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Target className="h-4 w-4 text-primary" />
              Goals
            </CardTitle>
            <CardDescription>
              Completion criteria the controller evaluates during the run.
            </CardDescription>
          </CardHeader>
          <CardContent className="p-0">
            {tasks.length > 0 ? (
              <div className="divide-y divide-border">
                {tasks.map((task, index) => (
                  <div key={task.name} className="flex gap-3 px-5 py-3">
                    <div className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-primary/10 text-xs font-semibold text-primary">
                      {index + 1}
                    </div>
                    <div className="min-w-0">
                      <div className="text-sm font-medium text-foreground">
                        {task.name}
                      </div>
                      {task.description ? (
                        <div className="mt-0.5 text-xs leading-relaxed text-muted-foreground">
                          {task.description}
                        </div>
                      ) : null}
                    </div>
                  </div>
                ))}
              </div>
            ) : (
              <div className="px-5 py-4 text-sm text-muted-foreground">
                No goals are defined.
              </div>
            )}
          </CardContent>
        </Card>
      </div>

      <DAGAttributes dag={dag} />

      <Card className="gap-0 overflow-hidden">
        <CardHeader>
          <CardTitle>Available actions</CardTitle>
          <CardDescription>
            Select an action to inspect its complete configuration.
          </CardDescription>
        </CardHeader>
        <CardContent className="p-0">
          {actions.length > 0 ? (
            <div>
              <div className="hidden grid-cols-[minmax(260px,1.4fr)_minmax(150px,0.7fr)_minmax(180px,0.8fr)_20px] gap-4 border-b border-border bg-surface-variant/40 px-5 py-2 text-xs font-semibold text-muted-foreground md:grid">
                <div>Action</div>
                <div>Execution</div>
                <div>Configuration</div>
                <div />
              </div>
              <div className="divide-y divide-border">
                {actions.map((step) => {
                  const execution = getExecutionSummary(step);
                  const configuration = getConfigurationSummary(step);

                  return (
                    <button
                      key={step.name}
                      type="button"
                      className="grid w-full gap-3 px-5 py-3 text-left transition-colors hover:bg-muted/40 focus-visible:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary md:grid-cols-[minmax(260px,1.4fr)_minmax(150px,0.7fr)_minmax(180px,0.8fr)_20px] md:items-center md:gap-4"
                      onClick={() => onSelectStep(step)}
                    >
                      <div className="min-w-0">
                        <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                          <span className="break-words text-sm font-semibold text-foreground">
                            {step.name}
                          </span>
                          {step.id ? (
                            <span className="truncate font-mono text-[11px] text-muted-foreground">
                              ID: {step.id}
                            </span>
                          ) : null}
                        </div>
                        {step.description ? (
                          <div className="mt-1 line-clamp-2 text-xs leading-relaxed text-muted-foreground">
                            {step.description}
                          </div>
                        ) : null}
                      </div>

                      <div className="min-w-0">
                        <div className="mb-1 text-[11px] font-medium uppercase text-muted-foreground md:hidden">
                          Execution
                        </div>
                        <div className="flex min-w-0 items-center gap-2">
                          <Badge
                            variant={step.call ? 'info' : 'outline'}
                            className="max-w-full"
                          >
                            {step.call ? (
                              <GitBranch className="h-3 w-3" />
                            ) : null}
                            {execution.label}
                          </Badge>
                          {execution.detail ? (
                            <span
                              className="truncate text-xs text-muted-foreground"
                              title={execution.detail}
                            >
                              {execution.detail}
                            </span>
                          ) : null}
                        </div>
                      </div>

                      <div className="min-w-0">
                        <div className="mb-1 text-[11px] font-medium uppercase text-muted-foreground md:hidden">
                          Configuration
                        </div>
                        <div className="flex flex-wrap gap-1.5">
                          {configuration.map((item) => (
                            <Badge key={item} variant="default">
                              {item}
                            </Badge>
                          ))}
                        </div>
                      </div>

                      <ChevronRight className="hidden h-4 w-4 text-muted-foreground md:block" />
                    </button>
                  );
                })}
              </div>
            </div>
          ) : (
            <div className="px-5 py-4 text-sm text-muted-foreground">
              No actions are defined.
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
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

function getConfigurationSummary(step: Step): string[] {
  const items: string[] = [];
  if (step.params) items.push('Parameters');
  if (step.repeatPolicy?.repeat) {
    items.push(`Repeat ${step.repeatPolicy.repeat}`);
  }
  if (step.timeoutSec) items.push(`${step.timeoutSec}s timeout`);
  if (step.preconditions?.length) {
    items.push(
      `${step.preconditions.length} condition${step.preconditions.length === 1 ? '' : 's'}`
    );
  }
  if (step.output || step.outputs?.length) items.push('Outputs');
  return items.length > 0 ? items : ['Default'];
}

function formatLabel(value: string): string {
  const label = value.replace(/[-_]+/g, ' ');
  return label.charAt(0).toUpperCase() + label.slice(1);
}
