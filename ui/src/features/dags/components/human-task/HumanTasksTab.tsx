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
import { AlertTriangle, Check, Info, RefreshCcw } from 'lucide-react';
import React from 'react';

import { components } from '../../../../api/v1/schema';
import type { JSONSchema } from '../../../../lib/schema-utils';
import { buildParamSchemaUiSchema } from '../dag-execution/paramSchemaForm';
import { schemaFormTemplates } from '../dag-execution/schemaFormTemplates';
import { schemaFormWidgets } from '../dag-execution/schemaFormWidgets';
import { ArtifactFilePreview } from '../artifacts/ArtifactFilePreview';
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
  const [formData, setFormData] = React.useState<FormData>({});
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
  const completionDisabled =
    !canExecute || !runWaiting || submitting || !node.step.id;
  const uiSchema = React.useMemo<UiSchema<FormData>>(
    () => ({
      ...(schema ? buildParamSchemaUiSchema(schema) : {}),
      'ui:submitButtonOptions': { norender: true },
    }),
    [schema]
  );

  const complete = async (input: FormData) => {
    if (!node.step.id || submitting) return;
    if (hasUnsafeInteger(input)) {
      setError(
        'This form cannot submit integers outside the safe integer range. Use the CLI or a raw API request for larger integers.'
      );
      return;
    }
    setSubmitting(true);
    setError(null);
    try {
      const { error: requestError } = await client.POST(
        '/dag-runs/{name}/{dagRunId}/human-tasks/{stepId}/complete',
        {
          params: {
            path: {
              name: dagRun.name,
              dagRunId: dagRun.dagRunId,
              stepId: node.step.id,
            },
            query: { remoteNode },
          },
          body: input,
        }
      );
      if (requestError) {
        setError(
          errorMessage(requestError, 'Failed to complete the human task.')
        );
        return;
      }
    } catch (requestError) {
      setError(
        errorMessage(requestError, 'Failed to complete the human task.')
      );
    } finally {
      setSubmitting(false);
      onChanged();
    }
  };

  return (
    <div className="space-y-4 rounded-lg border border-border bg-surface p-4">
      <div className="space-y-1">
        <div className="text-sm font-semibold">{node.step.name}</div>
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

      {error && (
        <Alert variant="destructive">
          <AlertTriangle className="h-4 w-4" />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {hasForm ? (
        <Form
          tagName="form"
          idPrefix={`human-task-${node.step.id ?? node.step.name}`}
          schema={schema as RJSFSchema}
          validator={validator}
          formData={formData}
          uiSchema={uiSchema}
          templates={schemaFormTemplates}
          widgets={schemaFormWidgets}
          disabled={completionDisabled}
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
          <div className="flex justify-end pt-2">
            <Button
              type="submit"
              variant="primary"
              disabled={completionDisabled}
            >
              <Check className="h-4 w-4" />
              {submitting ? <I18nText text={"Completing…"} /> : <I18nText text={"Complete task"} />}
            </Button>
          </div>
        </Form>
      ) : (
        <div className="flex justify-end">
          <Button
            type="button"
            variant="primary"
            disabled={completionDisabled}
            onClick={() => void complete({})}
          >
            <Check className="h-4 w-4" />
            {submitting ? <I18nText text={"Completing…"} /> : <I18nText text={"Complete task"} />}
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
          key={node.step.id ?? node.step.name}
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
