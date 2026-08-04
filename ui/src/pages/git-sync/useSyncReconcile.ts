// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useClient } from '@/hooks/api';
import { useErrorModal } from '@/components/ui/error-modal';
import { useSimpleToast } from '@/components/ui/simple-toast';
import { useState } from 'react';

interface UseSyncReconcileOptions {
  remoteNode: string;
  onSuccess: () => void;
}

export function useSyncReconcile({
  remoteNode,
  onSuccess,
}: UseSyncReconcileOptions) {
  const client = useClient();
  const { showToast } = useSimpleToast();
  const { showError } = useErrorModal();

  const [isForgetting, setIsForgetting] = useState(false);
  const [isDeleting, setIsDeleting] = useState(false);
  const [isMoving, setIsMoving] = useState(false);
  const [isCleaningUp, setIsCleaningUp] = useState(false);
  const [isDeletingMissing, setIsDeletingMissing] = useState(false);
  const [isDeletingBatch, setIsDeletingBatch] = useState(false);

  const handleForget = async (itemId: string) => {
    setIsForgetting(true);
    try {
      const response = await client.POST('/sync/items/{itemId}/forget', {
        params: { path: { itemId }, query: { remoteNode } },
      });
      if (response.error) {
        showError(response.error.message || 'Forget failed');
        return false;
      }
      showToast(`Removed ${itemId} from sync tracking`);
      onSuccess();
      return true;
    } catch (err) {
      showError(err instanceof Error ? err.message : 'Forget failed');
      return false;
    } finally {
      setIsForgetting(false);
    }
  };

  const handleDelete = async (itemId: string, force: boolean) => {
    setIsDeleting(true);
    try {
      const response = await client.POST('/sync/items/{itemId}/delete', {
        params: { path: { itemId }, query: { remoteNode } },
        body: { message: `Delete ${itemId}`, force },
      });
      if (response.error) {
        showError(response.error.message || 'Delete failed');
        return false;
      }
      showToast(`Deleted ${itemId}`);
      onSuccess();
      return true;
    } catch (err) {
      showError(err instanceof Error ? err.message : 'Delete failed');
      return false;
    } finally {
      setIsDeleting(false);
    }
  };

  const handleMove = async (
    itemId: string,
    newItemId: string,
    message: string,
    force: boolean
  ) => {
    setIsMoving(true);
    try {
      const response = await client.POST('/sync/items/{itemId}/move', {
        params: { path: { itemId }, query: { remoteNode } },
        body: { newItemId, message, force },
      });
      if (response.error) {
        showError(response.error.message || 'Move failed');
        return false;
      }
      showToast(`Moved ${itemId} to ${newItemId}`);
      onSuccess();
      return true;
    } catch (err) {
      showError(err instanceof Error ? err.message : 'Move failed');
      return false;
    } finally {
      setIsMoving(false);
    }
  };

  const handleCleanup = async () => {
    setIsCleaningUp(true);
    try {
      const response = await client.POST('/sync/cleanup', {
        params: { query: { remoteNode } },
      });
      if (response.error) {
        showError(response.error.message || 'Cleanup failed');
        return false;
      }
      const count = response.data?.forgotten?.length || 0;
      showToast(`Cleaned up ${count} missing item${count !== 1 ? 's' : ''}`);
      onSuccess();
      return true;
    } catch (err) {
      showError(err instanceof Error ? err.message : 'Cleanup failed');
      return false;
    } finally {
      setIsCleaningUp(false);
    }
  };

  const handleDeleteMissing = async (message: string) => {
    setIsDeletingMissing(true);
    try {
      const response = await client.POST('/sync/delete-missing', {
        params: { query: { remoteNode } },
        body: { message },
      });
      if (response.error) {
        showError(response.error.message || 'Delete missing failed');
        return false;
      }
      const count = response.data?.deleted?.length || 0;
      showToast(`Deleted ${count} missing item${count !== 1 ? 's' : ''}`);
      onSuccess();
      return true;
    } catch (err) {
      showError(err instanceof Error ? err.message : 'Delete missing failed');
      return false;
    } finally {
      setIsDeletingMissing(false);
    }
  };

  const handleDeleteBatch = async (
    itemIds: string[],
    message: string,
    force: boolean
  ) => {
    setIsDeletingBatch(true);
    try {
      const response = await client.POST('/sync/delete-batch', {
        params: { query: { remoteNode } },
        body: { itemIds, message, force },
      });
      if (response.error) {
        showError(response.error.message || 'Batch delete failed');
        return false;
      }
      const count = response.data?.deleted?.length || 0;
      showToast(`Deleted ${count} item${count !== 1 ? 's' : ''}`);
      onSuccess();
      return true;
    } catch (err) {
      showError(err instanceof Error ? err.message : 'Batch delete failed');
      return false;
    } finally {
      setIsDeletingBatch(false);
    }
  };

  return {
    isForgetting,
    isDeleting,
    isDeletingBatch,
    isMoving,
    isCleaningUp,
    isDeletingMissing,
    handleForget,
    handleDelete,
    handleDeleteBatch,
    handleMove,
    handleCleanup,
    handleDeleteMissing,
  };
}
