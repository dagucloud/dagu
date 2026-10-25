// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * LiveSwitch component provides a toggle switch for enabling/disabling DAG scheduling.
 *
 * @module features/dags/components/common
 */
import { useErrorModal } from '@/components/ui/error-modal';
import { Switch } from '@/components/ui/switch';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import ConfirmModal from '@/components/ui/confirm-dialog';
import { useCallback, useState } from 'react';
import { components } from '../../../../api/v1/schema';
import { useConfig } from '../../../../contexts/ConfigContext';
import { useClient } from '../../../../hooks/api';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';
import { isServiceDAG } from '@/lib/dagSchedule';

/**
 * Props for the LiveSwitch component
 */
type Props = {
  /** DAG file information */
  dag: components['schemas']['DAGFile'];
  /** Function to refresh data after toggling */
  refresh?: () => void;
  /**
   * Accessible name; defaults to "Schedule for" and the workflow name, or
   * "Restarts for" and the workflow name for a DAG kept alive by a restart
   * policy.
   */
  'aria-label'?: string;
};

/**
 * Switch component for toggling DAG suspension state
 * When enabled (checked), the DAG is active and can be scheduled
 * When disabled (unchecked), the DAG is suspended and won't be scheduled
 * For a DAG with a restart policy, the switch suspends its restarts instead.
 */
function LiveSwitch({ dag, refresh, 'aria-label': ariaLabel }: Props) {
  const { ts } = useI18n();
  const client = useClient();
  const config = useConfig();
  const { showError } = useErrorModal();
  const [checked, setChecked] = useState(!dag.suspended);
  const [showConfirm, setShowConfirm] = useState(false);
  const [pendingState, setPendingState] = useState<boolean | null>(null);
  const remoteNode = useRemoteNode();
  const isService = isServiceDAG(dag.dag);

  const onSubmit = useCallback(
    async (suspend: boolean) => {
      const { error } = await client.POST('/dags/{fileName}/suspend', {
        params: {
          path: {
            fileName: dag.fileName,
          },
          query: {
            remoteNode,
          },
        },
        body: {
          suspend,
        },
      });
      if (error) {
        showError(
          error.message || 'Failed to update DAG status',
          'Please try again or check the server connection.'
        );
        return;
      }
      if (refresh) {
        refresh();
      }
    },
    [client, dag.fileName, refresh, remoteNode, showError]
  );

  const handleCheckedChange = useCallback((newCheckedState: boolean) => {
    setPendingState(newCheckedState);
    setShowConfirm(true);
  }, []);

  const handleConfirm = useCallback(() => {
    if (pendingState !== null) {
      setChecked(pendingState);
      onSubmit(!pendingState);
    }
    setShowConfirm(false);
    setPendingState(null);
  }, [pendingState, onSubmit]);

  const handleCancel = useCallback(() => {
    setShowConfirm(false);
    setPendingState(null);
  }, []);

  return (
    <>
      <Switch
        checked={checked}
        onCheckedChange={
          config.permissions.runDags ? handleCheckedChange : undefined
        }
        disabled={!config.permissions.runDags}
        aria-label={
          ariaLabel ??
          ts(isService ? 'Restarts for {name}' : 'Schedule for {name}', {
            name: dag.dag.name,
          })
        }
      />
      <I18nProps>
        <ConfirmModal
          title={
            isService
              ? pendingState
                ? 'Resume restarts'
                : 'Suspend restarts'
              : pendingState
                ? 'Enable schedule'
                : 'Disable schedule'
          }
          buttonText={
            isService
              ? pendingState
                ? 'Resume'
                : 'Suspend'
              : pendingState
                ? 'Enable'
                : 'Disable'
          }
          visible={showConfirm}
          dismissModal={handleCancel}
          onSubmit={handleConfirm}
        >
          <p>
            {isService
              ? ts(
                  pendingState
                    ? 'Resume the restarts of "{name}"? The scheduler starts a run when one is due.'
                    : 'Suspend the restarts of "{name}"? A running run keeps running.',
                  { name: dag.fileName }
                )
              : ts(
                  'Are you sure you want to {action} the schedule for "{name}"?',
                  {
                    action: ts(pendingState ? 'enable' : 'disable'),
                    name: dag.fileName,
                  }
                )}
          </p>
        </ConfirmModal>
      </I18nProps>
    </>
  );
}

export default LiveSwitch;
