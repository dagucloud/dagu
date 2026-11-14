// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { useSimpleToast } from '@/components/ui/simple-toast';
import { Spinner } from '@/components/ui/spinner';
import { useConfig } from '@/contexts/ConfigContext';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useClient } from '@/hooks/api';
import { I18nTemplate } from '@/i18n/I18nTemplate';
import { I18nText } from '@/i18n/I18nText';
import { Play, X } from 'lucide-react';
import { useContext, useState } from 'react';
import { components } from '../../../../api/v1/schema';
import { DAGContext } from '../../contexts/DAGContext';

type Props = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** The run that holds the step. A child run retries through its root run. */
  dagRun: components['schemas']['DAGRunDetails'];
  stepName: string;
  /** Disables the Retry button, for example while the run is running. */
  disabled?: boolean;
  /** Called after the retry starts. Without it, the DAG context refreshes. */
  onRetried?: () => void;
};

/**
 * Asks to retry one step, with the retry scope and the precondition bypass.
 * The options reset each time the dialog opens.
 */
export function StepRetryDialog({
  open,
  onOpenChange,
  dagRun,
  stepName,
  disabled = false,
  onRetried,
}: Props) {
  const client = useClient();
  const config = useConfig();
  const dagContext = useContext(DAGContext);
  const remoteNode = useRemoteNode();
  const { showToast } = useSimpleToast();
  const [includeDownstream, setIncludeDownstream] = useState(false);
  const [bypassPreconditions, setBypassPreconditions] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Adjusted during render so a reopened dialog never shows the old options.
  const [prevOpen, setPrevOpen] = useState(open);
  if (prevOpen !== open) {
    setPrevOpen(open);
    setError(null);
    if (open) {
      setIncludeDownstream(false);
      setBypassPreconditions(false);
    }
  }

  const isSubDAGRun =
    !!dagRun.rootDAGRunId &&
    !!dagRun.rootDAGRunName &&
    dagRun.rootDAGRunId !== dagRun.dagRunId;

  const handleRetry = async () => {
    if (!config.permissions.runDags) {
      return;
    }

    setLoading(true);
    setError(null);
    try {
      const retryDAGName = isSubDAGRun ? dagRun.rootDAGRunName : dagRun.name;
      const retryDAGRunId = isSubDAGRun ? dagRun.rootDAGRunId : dagRun.dagRunId;
      const { error: requestError } = await client.POST(
        '/dag-runs/{name}/{dagRunId}/retry',
        {
          params: {
            path: { name: retryDAGName, dagRunId: retryDAGRunId },
            query: { remoteNode },
          },
          body: {
            dagRunId: retryDAGRunId,
            stepName,
            ...(includeDownstream ? { includeDownstream: true } : {}),
            ...(bypassPreconditions ? { bypassPreconditions: true } : {}),
            ...(isSubDAGRun ? { subDAGRunId: dagRun.dagRunId } : {}),
          },
        }
      );
      if (requestError) {
        setError(requestError.message || 'Failed to retry DAG run');
        return;
      }
      onOpenChange(false);
      showToast('Step retry started');
      if (onRetried) {
        onRetried();
      } else {
        dagContext.refresh();
      }
    } catch (e) {
      const requestError = e as {
        data?: { message?: string };
        message?: string;
      };
      setError(
        requestError?.data?.message ||
          requestError.message ||
          'Failed to retry DAG run'
      );
    } finally {
      setLoading(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            <I18nText text={'Retry this step?'} />
          </DialogTitle>
        </DialogHeader>
        <div className="py-2 text-sm space-y-3">
          <p>
            <I18nTemplate
              text="This will re-execute {step}. Are you sure?"
              values={{ step: <b>{stepName}</b> }}
            />
          </p>
          <p className="text-xs text-muted-foreground">
            <I18nTemplate
              text="Retries with the definition of run {id}"
              values={{
                id: (
                  <span className="font-mono">
                    {isSubDAGRun ? dagRun.rootDAGRunId : dagRun.dagRunId}
                  </span>
                ),
              }}
            />
          </p>
          <fieldset className="space-y-1.5">
            <legend className="text-xs text-muted-foreground">
              <I18nText text={'Retry scope'} />
            </legend>
            <label className="flex items-start gap-2 cursor-pointer">
              <input
                type="radio"
                name="retry-scope"
                className="mt-0.5"
                checked={!includeDownstream}
                onChange={() => setIncludeDownstream(false)}
              />
              <span>
                <span className="font-medium">
                  <I18nText text={'This step only'} />
                </span>
                <span className="block text-xs text-muted-foreground">
                  <I18nText
                    text={'Downstream steps keep their current status.'}
                  />
                </span>
              </span>
            </label>
            <label className="flex items-start gap-2 cursor-pointer">
              <input
                type="radio"
                name="retry-scope"
                className="mt-0.5"
                checked={includeDownstream}
                onChange={() => setIncludeDownstream(true)}
              />
              <span>
                <span className="font-medium">
                  <I18nText text={'This step and all downstream steps'} />
                </span>
                <span className="block text-xs text-muted-foreground">
                  <I18nText
                    text={
                      'Reset this step and every reachable descendant. Unrelated branches are left unchanged.'
                    }
                  />
                </span>
              </span>
            </label>
          </fieldset>
          <label className="flex items-start gap-2 cursor-pointer">
            <input
              type="checkbox"
              className="mt-0.5"
              checked={bypassPreconditions}
              onChange={(e) => setBypassPreconditions(e.target.checked)}
            />
            <span>
              <span className="font-medium">
                <I18nText text={'Bypass step preconditions'} />
              </span>
              <span className="block text-xs text-muted-foreground">
                <I18nText
                  text={
                    'Skip precondition checks for the retried steps. Workflow-level preconditions still apply.'
                  }
                />
              </span>
            </span>
          </label>
          {error && <div className="text-error mt-2">{error}</div>}
        </div>
        <DialogFooter>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => onOpenChange(false)}
            disabled={loading}
          >
            <X className="h-4 w-4" />
            <I18nText text={'Cancel'} />
          </Button>
          <Button
            size="sm"
            onClick={handleRetry}
            disabled={loading || disabled || !config.permissions.runDags}
          >
            {loading ? (
              <Spinner className="size-4" />
            ) : (
              <Play className="h-4 w-4" />
            )}
            <I18nText text={'Retry'} />
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export default StepRetryDialog;
