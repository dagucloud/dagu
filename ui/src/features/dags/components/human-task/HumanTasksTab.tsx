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
import { AlertTriangle, Check, RefreshCcw } from 'lucide-react';
import React from 'react';

import { components } from '../../../../api/v1/schema';
import type { JSONSchema } from '../../../../lib/schema-utils';
import { buildParamSchemaUiSchema } from '../dag-execution/paramSchemaForm';
import { schemaFormTemplates } from '../dag-execution/schemaFormTemplates';
import { schemaFormWidgets } from '../dag-execution/schemaFormWidgets';

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

function HumanTaskCard({
  node,
  dagRun,
  canExecute,
  onChanged,
}: {
  node: HumanTaskNode;
  dagRun: DAGRunDetails;
  canExecute: boolean;
  onChanged: () => void;
}) {
  const client = useClient();
  const remoteNode = useRemoteNode();
  const [formData, setFormData] = React.useState<FormData>({});
  const [submitting, setSubmitting] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const task = node.step.humanTask!;
  const schema = (task.form ?? undefined) as JSONSchema | undefined;
  const hasForm = !!schema && Object.keys(schema).length > 0;
  const uiSchema = React.useMemo<UiSchema<FormData>>(
    () => ({
      ...(schema ? buildParamSchemaUiSchema(schema) : {}),
      'ui:submitButtonOptions': { norender: true },
    }),
    [schema]
  );

  const complete = async (input: FormData) => {
    if (!node.step.id || submitting) return;
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

      {error && (
        <Alert variant="destructive">
          <AlertTriangle className="h-4 w-4" />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {hasForm ? (
        <Form
          tagName="form"
          schema={schema as RJSFSchema}
          validator={validator}
          formData={formData}
          uiSchema={uiSchema}
          templates={schemaFormTemplates}
          widgets={schemaFormWidgets}
          disabled={!canExecute || submitting}
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
              disabled={!canExecute || submitting}
            >
              <Check className="h-4 w-4" />
              {submitting ? 'Completing…' : 'Complete task'}
            </Button>
          </div>
        </Form>
      ) : (
        <div className="flex justify-end">
          <Button
            type="button"
            variant="primary"
            disabled={!canExecute || submitting || !node.step.id}
            onClick={() => void complete({})}
          >
            <Check className="h-4 w-4" />
            {submitting ? 'Completing…' : 'Complete task'}
          </Button>
        </div>
      )}

      {!canExecute && (
        <p className="text-xs text-muted-foreground">
          Execute permission is required to complete this task.
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
  const { waitingHumanTaskNodes: waitingTasks } =
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
        No human tasks are waiting.
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
              Task input is safely stored, but the DAG-run still needs to be
              queued for resume.
            </span>
            <Button
              type="button"
              size="sm"
              variant="outline"
              disabled={!canExecute || resuming}
              onClick={() => void resume()}
            >
              <RefreshCcw className="h-4 w-4" />
              {resuming ? 'Queueing…' : 'Retry queue'}
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

      {waitingTasks.map((node) => (
        <HumanTaskCard
          key={node.step.id ?? node.step.name}
          node={node}
          dagRun={dagRun}
          canExecute={canExecute}
          onChanged={onChanged}
        />
      ))}
    </div>
  );
}
