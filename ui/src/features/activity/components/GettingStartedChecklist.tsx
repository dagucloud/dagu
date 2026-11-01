// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { CircleCheck, ExternalLink, X } from 'lucide-react';
import React, { useId } from 'react';
import { Link } from 'react-router-dom';
import { Button } from '@/components/ui/button';
import CreateDAGButton from '@/features/dags/components/common/CreateDAGButton';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import type { GettingStartedStep } from '../hooks/useGettingStarted';

const SCHEDULING_DOCS_URL = 'https://docs.dagu.sh/writing-workflows/scheduling';

const STEP_TITLES: Record<GettingStartedStep['id'], string> = {
  run: 'Run a workflow',
  create: 'Create your own workflow',
  schedule: 'Put a workflow on a schedule',
  alert: 'Get alerted when something fails',
};

function stepDetail(step: GettingStartedStep): string {
  switch (step.id) {
    case 'run':
      return step.exampleFile
        ? 'The retry example fails once, then succeeds on its retry.'
        : 'Start a workflow from the Workflows page.';
    case 'create':
      return 'Define steps in YAML. Each step runs a command.';
    case 'schedule':
      return 'A schedule runs a workflow while no one is watching.';
    case 'alert':
      return step.canConnect
        ? 'Get an email from Dagu Console when a workflow fails or this server stops, or send failures to a notification channel.'
        : 'Send failures to Slack, email, or a webhook.';
  }
}

function StepActions({ step }: { step: GettingStartedStep }) {
  const { ts } = useI18n();
  switch (step.id) {
    case 'run':
      return (
        <Button asChild variant="outline">
          <Link to={step.exampleFile ? `/dags/${step.exampleFile}` : '/dags'}>
            {ts(step.exampleFile ? 'Open the retry example' : 'Open Workflows')}
          </Link>
        </Button>
      );
    case 'create':
      return <CreateDAGButton />;
    case 'schedule':
      return (
        <Button asChild variant="outline">
          <a href={SCHEDULING_DOCS_URL} target="_blank" rel="noreferrer">
            {ts('Scheduling guide')}
            <ExternalLink aria-hidden="true" />
          </a>
        </Button>
      );
    case 'alert':
      return (
        <>
          {step.canConnect && (
            <Button asChild variant="outline">
              <Link to="/license#monitoring">{ts('Connect for free')}</Link>
            </Button>
          )}
          {step.canAddChannel && (
            <Button asChild variant="outline">
              <Link to="/notification-channels">
                {ts('Add a notification channel')}
              </Link>
            </Button>
          )}
        </>
      );
  }
}

/**
 * Lists a new server's first steps with an action for each step left. Done
 * steps shrink to their title.
 */
export function GettingStartedChecklist({
  steps,
  onDismiss,
}: {
  steps: GettingStartedStep[];
  onDismiss: () => void;
}): React.ReactElement {
  const { ts } = useI18n();
  const headingId = useId();
  const done = steps.filter((step) => step.done).length;

  return (
    <section
      aria-labelledby={headingId}
      className="rounded-lg border border-border bg-card"
    >
      <div className="flex items-center gap-2 border-b border-border px-3 py-2">
        <h2 id={headingId} className="text-sm font-medium text-foreground">
          {ts('Getting started')}
        </h2>
        <span className="text-xs text-muted-foreground tabular-nums">
          {ts('{done} of {total} done', { done, total: steps.length })}
        </span>
        <button
          type="button"
          onClick={onDismiss}
          className="ml-auto rounded p-0.5 text-muted-foreground hover:text-foreground"
          aria-label={ts('Dismiss')}
        >
          <X className="h-4 w-4" />
        </button>
      </div>
      <ol className="divide-y divide-border">
        {steps.map((step, index) => (
          <li key={step.id} className="flex items-start gap-3 px-3 py-2.5">
            {step.done ? (
              <CircleCheck
                className="mt-0.5 h-4 w-4 shrink-0 text-success"
                aria-hidden="true"
              />
            ) : (
              <span
                className="mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded-full border border-border text-[10px] tabular-nums text-muted-foreground"
                aria-hidden="true"
              >
                {index + 1}
              </span>
            )}
            <div className="flex min-w-0 flex-1 flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
              <div className="min-w-0">
                <p
                  className={cn(
                    'text-sm',
                    step.done
                      ? 'text-muted-foreground'
                      : 'font-medium text-foreground'
                  )}
                >
                  {ts(STEP_TITLES[step.id])}
                  {step.done && (
                    <span className="sr-only"> ({ts('Done')})</span>
                  )}
                </p>
                {!step.done && (
                  <p className="text-xs text-muted-foreground">
                    {ts(stepDetail(step))}
                  </p>
                )}
              </div>
              {!step.done && (
                <div className="flex flex-wrap items-center gap-2">
                  <StepActions step={step} />
                </div>
              )}
            </div>
          </li>
        ))}
      </ol>
    </section>
  );
}
