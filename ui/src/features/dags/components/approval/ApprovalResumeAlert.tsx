// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useState } from 'react';

import { components } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { useCanExecuteForWorkspace } from '@/contexts/AuthContext';
import { useConfig } from '@/contexts/ConfigContext';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useClient } from '@/hooks/api';
import { I18nText } from '@/i18n/I18nText';

export function ApprovalResumeAlert({
  dagRun,
}: {
  dagRun: components['schemas']['DAGRunDetails'];
}) {
  const client = useClient();
  const remoteNode = useRemoteNode();
  const config = useConfig();
  const canExecute = useCanExecuteForWorkspace(dagRun.workspace);
  const [resuming, setResuming] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const resume = async () => {
    setResuming(true);
    setError(null);
    try {
      const { error } = await client.POST(
        '/dag-runs/{name}/{dagRunId}/resume',
        {
          params: {
            path: { name: dagRun.name, dagRunId: dagRun.dagRunId },
            query: { remoteNode },
          },
        }
      );
      if (error) throw new Error(error.message || 'Failed to resume DAG-run');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to resume DAG-run');
    } finally {
      setResuming(false);
    }
  };

  return (
    <div
      role="alert"
      className="rounded border border-warning/40 bg-warning/5 p-3 space-y-2"
    >
      <div className="flex items-center justify-between gap-3">
        <span>
          <I18nText text="Approval saved; resume failed." />
        </span>
        <Button
          size="sm"
          variant="outline"
          disabled={!config.permissions.runDags || !canExecute || resuming}
          onClick={resume}
        >
          <I18nText text={resuming ? 'Resuming...' : 'Retry resume'} />
        </Button>
      </div>
      {error && <p className="text-sm text-error">{error}</p>}
    </div>
  );
}
