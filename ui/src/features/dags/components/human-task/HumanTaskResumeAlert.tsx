// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { AlertTriangle, RefreshCcw } from 'lucide-react';
import { useState } from 'react';
import { components } from '@/api/v1/schema';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { useCanExecuteForWorkspace } from '@/contexts/AuthContext';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useClient } from '@/hooks/api';
import { I18nText } from '@/i18n/I18nText';
import { getQueryErrorMessage } from '@/lib/queryErrors';

const RESUME_FAILED = 'Failed to queue the DAG-run for resume.';

/**
 * The retry of a run whose completed human tasks are stored but whose
 * resume failed to be queued.
 */
export function HumanTaskResumeAlert({
  dagRun,
  onChanged,
}: {
  dagRun: components['schemas']['DAGRunDetails'];
  onChanged: () => void;
}) {
  const client = useClient();
  const remoteNode = useRemoteNode();
  const canExecute = useCanExecuteForWorkspace(dagRun.workspace);
  const [resuming, setResuming] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const resume = async () => {
    if (resuming) return;
    setResuming(true);
    setError(null);
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
      if (error) setError(getQueryErrorMessage(error, RESUME_FAILED));
    } catch (caught) {
      setError(getQueryErrorMessage(caught, RESUME_FAILED));
    } finally {
      setResuming(false);
      onChanged();
    }
  };

  return (
    <>
      <Alert variant="warning">
        <AlertTriangle className="h-4 w-4" />
        <AlertDescription className="flex flex-wrap items-center justify-between gap-3">
          <span>
            <I18nText
              text={
                'Task input is safely stored, but the DAG-run still needs to be queued for resume.'
              }
            />
          </span>
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={!canExecute || resuming}
            onClick={() => void resume()}
          >
            <RefreshCcw className="h-4 w-4" />
            {resuming ? (
              <I18nText text={'Queueing…'} />
            ) : (
              <I18nText text={'Retry queue'} />
            )}
          </Button>
        </AlertDescription>
      </Alert>
      {error && (
        <Alert variant="destructive">
          <AlertTriangle className="h-4 w-4" />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
    </>
  );
}
