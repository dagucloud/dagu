// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useRef, useState } from 'react';
import { RotateCcw } from 'lucide-react';
import { Status } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import ConfirmModal from '@/components/ui/confirm-dialog';
import { useErrorModal } from '@/components/ui/error-modal';
import { useSimpleToast } from '@/components/ui/simple-toast';
import { useConfig } from '@/contexts/ConfigContext';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { isSubDAGRun } from '@/features/dags/hooks/useStepLogQuery';
import { useClient } from '@/hooks/api';
import { useI18n } from '@/i18n/I18nProvider';
import type { ApiDAGRunDetails } from '../data/types';

/** Run statuses a retry cannot start from, as the run's own Retry has them. */
const NOT_RETRIABLE: readonly Status[] = [
  Status.Running,
  Status.Queued,
  Status.Waiting,
];

/**
 * Retries a root run whose steps with nodes left `count` nodes that did not
 * succeed (RFC 013 §Retry). The retry runs those nodes again and keeps the
 * results of the nodes that succeeded. It shows nothing for a sub-run, which
 * retries through its root run, or for a run a retry cannot start from.
 */
export function RetryFailedNodes({
  details,
  count,
}: {
  details: ApiDAGRunDetails;
  count: number;
}) {
  const { ts } = useI18n();
  const config = useConfig();
  const client = useClient();
  const remoteNode = useRemoteNode();
  const { showToast } = useSimpleToast();
  const { showError } = useErrorModal();
  const [confirming, setConfirming] = useState(false);
  // One retry at a time: the ref guards a second submit before the button
  // turns disabled.
  const inFlight = useRef(false);
  const [pending, setPending] = useState(false);

  if (
    count === 0 ||
    !config.permissions.runDags ||
    isSubDAGRun(details) ||
    NOT_RETRIABLE.includes(details.status)
  ) {
    return null;
  }

  const retry = async () => {
    setConfirming(false);
    if (inFlight.current) return;
    inFlight.current = true;
    setPending(true);
    try {
      const { error } = await client.POST('/dag-runs/{name}/{dagRunId}/retry', {
        params: {
          path: { name: details.name, dagRunId: details.dagRunId },
          query: { remoteNode },
        },
        body: { dagRunId: details.dagRunId },
      });
      if (error) {
        showError(error.message || ts('Failed to retry the run'));
        return;
      }
      showToast(ts('Retry started'));
    } catch (e) {
      // The request did not reach the server, or timed out.
      showError(
        (e instanceof Error && e.message) || ts('Failed to retry the run')
      );
    } finally {
      inFlight.current = false;
      setPending(false);
    }
  };

  return (
    <>
      <Button
        type="button"
        variant="outline"
        size="xs"
        disabled={pending}
        onClick={() => setConfirming(true)}
      >
        <RotateCcw aria-hidden="true" />
        {ts('Retry failed nodes ({count})', { count })}
      </Button>
      <ConfirmModal
        title={ts('Retry failed nodes?')}
        buttonText={ts('Retry')}
        visible={confirming}
        dismissModal={() => setConfirming(false)}
        onSubmit={() => void retry()}
      >
        <p className="text-sm">
          {ts(
            'This retries the run. The {count} nodes that did not succeed run again, and nodes that succeeded keep their results. Other failed steps run again too.',
            { count }
          )}
        </p>
      </ConfirmModal>
    </>
  );
}
