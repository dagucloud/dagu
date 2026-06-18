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

/**
 * Props for the LiveSwitch component
 */
type Props = {
  /** DAG file information */
  dag: components['schemas']['DAGFile'];
  /** Function to refresh data after toggling */
  refresh?: () => void;
  /** Aria label for accessibility */
  'aria-label'?: string;
};

/**
 * Switch component for toggling DAG suspension state
 * When enabled (checked), the DAG is active and can be scheduled
 * When disabled (unchecked), the DAG is suspended and won't be scheduled
 */
function LiveSwitch({ dag, refresh, 'aria-label': ariaLabel }: Props) {
  const client = useClient();
  const config = useConfig();
  const { showError } = useErrorModal();
  const [checked, setChecked] = useState(!dag.suspended);
  const [showConfirm, setShowConfirm] = useState(false);
  const [pendingState, setPendingState] = useState<boolean | null>(null);
  const remoteNode = useRemoteNode();

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
        aria-label={ariaLabel}
      />
      <ConfirmModal
        title={pendingState ? 'Enable Schedule' : 'Disable Schedule'}
        buttonText={pendingState ? 'Enable' : 'Disable'}
        visible={showConfirm}
        dismissModal={handleCancel}
        onSubmit={handleConfirm}
      >
        <p>
          Are you sure you want to {pendingState ? 'enable' : 'disable'} the
          schedule for &quot;{dag.fileName}&quot;?
        </p>
      </ConfirmModal>
    </>
  );
}

export default LiveSwitch;
