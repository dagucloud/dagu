// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { useCanExecuteForWorkspace } from '@/contexts/AuthContext';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { getManualActionState } from '@/features/dag-runs/lib/manualActionState';
import { useClient } from '@/hooks/api';
import type { IChangeEvent } from '@rjsf/core';
import Form from '@rjsf/shadcn';
import type { RJSFSchema, UiSchema } from '@rjsf/utils';
import validator from '@rjsf/validator-ajv8';
import {
  AlertTriangle,
  Check,
  Info,
  RefreshCcw,
  RotateCcw,
  X,
} from 'lucide-react';
import React from 'react';

import { components } from '../../../../api/v1/schema';
import type { JSONSchema } from '../../../../lib/schema-utils';
import { buildParamSchemaUiSchema } from '../dag-execution/paramSchemaForm';
import { schemaFormTemplates } from '../dag-execution/schemaFormTemplates';
import { schemaFormWidgets } from '../dag-execution/schemaFormWidgets';
import { ArtifactFilePreview } from '../artifacts/ArtifactFilePreview';
import PushBackHistory from '../common/PushBackHistory';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';
import { Tab, Tabs } from '@/components/ui/tabs';

type DAGRunDetails = components['schemas']['DAGRunDetails'];
type HumanTaskNode = components['schemas']['Node'];
type FormData = Record<string, unknown>;

interface HumanTasksTabProps {
  dagRun: DAGRunDetails;
  onChanged: () => void;
}

function errorMessage(error: unknown, fallback: string): string {
  if (
    typeof error === 'object' &&
    error !== null &&
    'message' in error &&
    typeof error.message === 'string'
  ) {
    return error.message;
  }
  return fallback;
}

function hasUnsafeInteger(value: unknown): boolean {
  if (typeof value === 'number') {
    return Number.isInteger(value) && !Number.isSafeInteger(value);
  }
  if (Array.isArray(value)) {
    return value.some(hasUnsafeInteger);
  }
  if (typeof value === 'object' && value !== null) {
    return Object.values(value).some(hasUnsafeInteger);
  }
  return false;
}

type CardMode = 'complete' | 'push-back';

const unsafeIntegerMessage =
  'This form cannot submit integers outside the safe integer range. Use the CLI or a raw API request for larger integers.';

function useTaskFormUiSchema(schema?: JSONSchema): UiSchema<FormData> {
  return React.useMemo<UiSchema<FormData>>(
    () => ({
      ...(schema ? buildParamSchemaUiSchema(schema) : {}),
      'ui:submitButtonOptions': { norender: true },
    }),
    [schema]
  );
}

function HumanTaskCard({
  node,
  dagRun,
  canExecute,
  runWaiting,
  onChanged,
}: {
  node: HumanTaskNode;
  dagRun: DAGRunDetails;
  canExecute: boolean;
  runWaiting: boolean;
  onChanged: () => void;
}) {
  const client = useClient();
  const remoteNode = useRemoteNode();
  const { ts } = useI18n();
  const [mode, setMode] = React.useState<CardMode>('complete');
  const [formData, setFormData] = React.useState<FormData>({});
  const [feedbackData, setFeedbackData] = React.useState<FormData>({});
  const [submitting, setSubmitting] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  // A completion rejected while the run was executing no longer applies once
  // the run is waiting again.
  React.useEffect(() => {
    if (runWaiting) {
      setError(null);
    }
  }, [runWaiting]);
  const task = node.step.humanTask!;
  const iteration = node.approvalIteration ?? 0;
  const pushBack = task.pushBack;
  const artifacts = task.artifacts ?? [];
  const [selectedArtifact, setSelectedArtifact] = React.useState<string | null>(
    null
  );
  const activeArtifact =
    selectedArtifact && artifacts.includes(selectedArtifact)
      ? selectedArtifact
      : (artifacts[0] ?? null);
  const schema = (task.form ?? undefined) as JSONSchema | undefined;
  const hasForm = !!schema && Object.keys(schema).length > 0;
  const feedbackSchema = (pushBack?.form ?? undefined) as
    | JSONSchema
    | undefined;
  const hasFeedbackForm =
    !!feedbackSchema && Object.keys(feedbackSchema).length > 0;
  const actionsDisabled =
    !canExecute || !runWaiting || submitting || !node.step.id;
  const uiSchema = useTaskFormUiSchema(schema);
  const feedbackUiSchema = useTaskFormUiSchema(feedbackSchema);
  const stepKey = node.step.id ?? node.step.name;

  const send = async (
    input: FormData,
    fallback: string,
    request: (stepId: string) => Promise<{ error?: unknown }>
  ) => {
    if (!node.step.id || submitting) return;
    if (hasUnsafeInteger(input)) {
      setError(unsafeIntegerMessage);
      return;
    }
    setSubmitting(true);
    setError(null);
    try {
      const { error: requestError } = await request(node.step.id);
      if (requestError) {
        setError(errorMessage(requestError, fallback));
      }
    } catch (requestError) {
      setError(errorMessage(requestError, fallback));
    } finally {
      setSubmitting(false);
      onChanged();
    }
  };

  const complete = (input: FormData) =>
    send(input, 'Failed to complete the human task.', (stepId) =>
      client.POST('/dag-runs/{name}/{dagRunId}/human-tasks/{stepId}/complete', {
        params: {
          path: { name: dagRun.name, dagRunId: dagRun.dagRunId, stepId },
          query: { remoteNode },
        },
        body: input,
      })
    );

  // The expected iteration makes a stale page fail instead of pushing back a
  // task that already reopened.
  const requestChanges = (input: FormData) =>
    send(input, 'Failed to push back the human task.', (stepId) =>
      client.POST(
        '/dag-runs/{name}/{dagRunId}/human-tasks/{stepId}/push-back',
        {
          params: {
            path: { name: dagRun.name, dagRunId: dagRun.dagRunId, stepId },
            query: { remoteNode, expectedIteration: iteration },
          },
          body: input,
        }
      )
    );

  const switchMode = (next: CardMode) => {
    setMode(next);
    setError(null);
  };

  const requestChangesButton = pushBack ? (
    <Button
      type="button"
      variant="outline"
      disabled={actionsDisabled}
      onClick={() => switchMode('push-back')}
    >
      <RotateCcw className="h-4 w-4" />
      <I18nText text={'Request changes'} />
    </Button>
  ) : null;

  const completeButtonLabel = submitting ? (
    <I18nText text={'Completing…'} />
  ) : (
    <I18nText text={'Complete task'} />
  );

  const pushBackButtonLabel = submitting ? (
    <I18nText text={'Requesting changes…'} />
  ) : (
    <I18nText text={'Request changes'} />
  );

  return (
    <div className="space-y-4 rounded-lg border border-border bg-surface p-4">
      <div className="space-y-1">
        <div className="flex items-center gap-2">
          <span className="text-sm font-semibold">{node.step.name}</span>
          {iteration > 0 && (
            <span className="rounded bg-muted px-1.5 py-0.5 text-xs font-normal text-muted-foreground">
              <I18nText text={'Iteration'} /> {iteration}
            </span>
          )}
        </div>
        <div className="whitespace-pre-wrap text-base">{task.prompt}</div>
      </div>

      {artifacts.length > 0 && (
        <div className="space-y-2">
          <div className="text-sm font-semibold">
            <I18nText text={'Artifacts'} />
          </div>
          {dagRun.artifactsAvailable ? (
            <>
              {artifacts.length > 1 && (
                <Tabs role="tablist" aria-label={ts('Task artifacts')}>
                  {artifacts.map((artifact) => (
                    <Tab
                      key={artifact}
                      role="tab"
                      aria-selected={activeArtifact === artifact}
                      isActive={activeArtifact === artifact}
                      onClick={() => setSelectedArtifact(artifact)}
                    >
                      {artifact}
                    </Tab>
                  ))}
                </Tabs>
              )}
              <ArtifactFilePreview
                dagRunName={dagRun.name}
                dagRunId={dagRun.dagRunId}
                path={activeArtifact}
                remoteNode={remoteNode}
              />
            </>
          ) : (
            <div className="rounded-lg border border-dashed border-border bg-muted/20 p-6 text-sm text-muted-foreground">
              <I18nText
                text={'Referenced artifacts are not available for this DAG run yet.'}
              />
            </div>
          )}
        </div>
      )}

      <PushBackHistory
        history={node.pushBackHistory}
        title={ts('Previous Push-backs')}
      />

      {error && (
        <Alert variant="destructive">
          <AlertTriangle className="h-4 w-4" />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {mode === 'push-back' && pushBack ? (
        <div className="space-y-3 rounded-md border border-border p-3">
          <div className="space-y-1">
            <div className="text-sm font-semibold">
              <I18nText text={'Request changes'} />
            </div>
            <p className="text-sm text-muted-foreground">
              <I18nText
                text={
                  '{step} and the steps that depend on it run again with your feedback. This task reopens afterward.'
                }
                values={{ step: pushBack.rewindTo }}
              />
            </p>
          </div>
          {hasFeedbackForm ? (
            <Form
              tagName="form"
              idPrefix={`human-task-${stepKey}-push-back`}
              schema={feedbackSchema as RJSFSchema}
              validator={validator}
              formData={feedbackData}
              uiSchema={feedbackUiSchema}
              templates={schemaFormTemplates}
              widgets={schemaFormWidgets}
              disabled={actionsDisabled}
              noHtml5Validate
              showErrorList={false}
              onChange={(event: IChangeEvent<FormData>) => {
                setFeedbackData((event.formData ?? {}) as FormData);
                setError(null);
              }}
              onSubmit={(event: IChangeEvent<FormData>) =>
                void requestChanges((event.formData ?? {}) as FormData)
              }
              onError={() =>
                setError(
                  'Fix the highlighted form errors before requesting changes.'
                )
              }
            >
              <div className="flex justify-end gap-2 pt-2">
                <Button
                  type="button"
                  variant="ghost"
                  disabled={submitting}
                  onClick={() => switchMode('complete')}
                >
                  <X className="h-4 w-4" />
                  <I18nText text={'Cancel'} />
                </Button>
                <Button
                  type="submit"
                  variant="primary"
                  disabled={actionsDisabled}
                >
                  <RotateCcw className="h-4 w-4" />
                  {pushBackButtonLabel}
                </Button>
              </div>
            </Form>
          ) : (
            <div className="flex justify-end gap-2">
              <Button
                type="button"
                variant="ghost"
                disabled={submitting}
                onClick={() => switchMode('complete')}
              >
                <X className="h-4 w-4" />
                <I18nText text={'Cancel'} />
              </Button>
              <Button
                type="button"
                variant="primary"
                disabled={actionsDisabled}
                onClick={() => void requestChanges({})}
              >
                <RotateCcw className="h-4 w-4" />
                {pushBackButtonLabel}
              </Button>
            </div>
          )}
        </div>
      ) : hasForm ? (
        <Form
          tagName="form"
          idPrefix={`human-task-${stepKey}`}
          schema={schema as RJSFSchema}
          validator={validator}
          formData={formData}
          uiSchema={uiSchema}
          templates={schemaFormTemplates}
          widgets={schemaFormWidgets}
          disabled={actionsDisabled}
          noHtml5Validate
          showErrorList={false}
          onChange={(event: IChangeEvent<FormData>) => {
            setFormData((event.formData ?? {}) as FormData);
            setError(null);
          }}
          onSubmit={(event: IChangeEvent<FormData>) =>
            void complete((event.formData ?? {}) as FormData)
          }
          onError={() =>
            setError(
              'Fix the highlighted form errors before completing the task.'
            )
          }
        >
          <div className="flex justify-end gap-2 pt-2">
            {requestChangesButton}
            <Button type="submit" variant="primary" disabled={actionsDisabled}>
              <Check className="h-4 w-4" />
              {completeButtonLabel}
            </Button>
          </div>
        </Form>
      ) : (
        <div className="flex justify-end gap-2">
          {requestChangesButton}
          <Button
            type="button"
            variant="primary"
            disabled={actionsDisabled}
            onClick={() => void complete({})}
          >
            <Check className="h-4 w-4" />
            {completeButtonLabel}
          </Button>
        </div>
      )}

      {!canExecute && (
        <p className="text-xs text-muted-foreground">
          <I18nText text={"Execute permission is required to complete this task."} />
        </p>
      )}
    </div>
  );
}

export function HumanTasksTab({ dagRun, onChanged }: HumanTasksTabProps) {
  const client = useClient();
  const remoteNode = useRemoteNode();
  const canExecute = useCanExecuteForWorkspace(dagRun.workspace);
  const [resuming, setResuming] = React.useState(false);
  const [resumeError, setResumeError] = React.useState<string | null>(null);
  const { isWaiting, waitingHumanTaskNodes: waitingTasks } =
    getManualActionState(dagRun);

  const resume = async () => {
    if (resuming) return;
    setResuming(true);
    setResumeError(null);
    try {
      const { error } = await client.POST(
        '/dag-runs/{name}/{dagRunId}/human-tasks/resume',
        {
          params: {
            path: { name: dagRun.name, dagRunId: dagRun.dagRunId },
            query: { remoteNode },
          },
        }
      );
      if (error) {
        setResumeError(
          errorMessage(error, 'Failed to queue the DAG-run for resume.')
        );
      }
    } catch (error) {
      setResumeError(
        errorMessage(error, 'Failed to queue the DAG-run for resume.')
      );
    } finally {
      setResuming(false);
      onChanged();
    }
  };

  if (waitingTasks.length === 0 && !dagRun.humanTaskResumePending) {
    return (
      <div className="py-8 text-center text-sm text-muted-foreground">
        <I18nText text={"No human tasks are waiting."} />
      </div>
    );
  }

  return (
    <div className="space-y-4">
      {dagRun.humanTaskResumePending && (
        <Alert variant="warning">
          <AlertTriangle className="h-4 w-4" />
          <AlertDescription className="flex flex-wrap items-center justify-between gap-3">
            <span>
              <I18nText text={"Task input is safely stored, but the DAG-run still needs to be queued for resume."} />
            </span>
            <Button
              type="button"
              size="sm"
              variant="outline"
              disabled={!canExecute || resuming}
              onClick={() => void resume()}
            >
              <RefreshCcw className="h-4 w-4" />
              {resuming ? <I18nText text={"Queueing…"} /> : <I18nText text={"Retry queue"} />}
            </Button>
          </AlertDescription>
        </Alert>
      )}

      {resumeError && (
        <Alert variant="destructive">
          <AlertTriangle className="h-4 w-4" />
          <AlertDescription>{resumeError}</AlertDescription>
        </Alert>
      )}

      {!isWaiting && waitingTasks.length > 0 && (
        <Alert variant="info">
          <Info className="h-4 w-4" />
          <AlertDescription>
            <I18nText text={"This DAG-run is queued or running. Open tasks become editable once it is waiting."} />
          </AlertDescription>
        </Alert>
      )}

      {waitingTasks.map((node) => (
        <HumanTaskCard
          key={`${node.step.id ?? node.step.name}-${node.approvalIteration ?? 0}`}
          node={node}
          dagRun={dagRun}
          canExecute={canExecute}
          runWaiting={isWaiting}
          onChanged={onChanged}
        />
      ))}
    </div>
  );
}
