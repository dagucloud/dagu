// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * DAGEditButtons component provides buttons for renaming and deleting a DAG.
 *
 * @module features/dags/components/dag-editor
 */
import { useCanWriteForWorkspace } from '@/contexts/AuthContext';
import { Button } from '@/components/ui/button';
import { useErrorModal } from '@/components/ui/error-modal';
import { PencilLine, Trash2 } from 'lucide-react';
import React from 'react';
import { DAGNameInputModal } from '../../../../components/DAGNameInputModal';
import { useRemoteNode } from '../../../../contexts/RemoteNodeContext';
import { useClient } from '../../../../hooks/api';

/**
 * Props for the DAGEditButtons component
 */
type Props = {
  /** DAG file name */
  fileName: string;
  /** Workspace label value for the DAG; empty/null means default. */
  workspace?: string | null;
};

/**
 * DAGEditButtons provides buttons for renaming and deleting a DAG
 */
function DAGEditButtons({ fileName, workspace }: Props) {
  const remoteNode = useRemoteNode();
  const canWrite = useCanWriteForWorkspace(workspace);
  const client = useClient();
  const { showError } = useErrorModal();
  const [isRenameModalOpen, setIsRenameModalOpen] = React.useState(false);
  const [renameError, setRenameError] = React.useState<string | null>(null);
  const [isRenameLoading, setIsRenameLoading] = React.useState(false);

  if (!canWrite) {
    return null;
  }

  const handleRenameClose = () => {
    setIsRenameModalOpen(false);
    setRenameError(null);
  };

  const handleRenameSubmit = async (newFileName: string) => {
    setIsRenameLoading(true);
    setRenameError(null);

    try {
      const { error } = await client.POST('/dags/{fileName}/rename', {
        params: {
          path: {
            fileName: fileName,
          },
          query: {
            remoteNode,
          },
        },
        body: {
          newFileName: newFileName,
        },
      });

      if (error) {
        setRenameError(error.message || 'An error occurred');
        setIsRenameLoading(false);
        return;
      }

      // Success - close modal and redirect
      setIsRenameModalOpen(false);

      // Redirect to the new DAG page
      const basePath = window.location.pathname.split('/dags')[0] || '';
      const searchParams = new URLSearchParams();
      searchParams.set('remoteNode', remoteNode);
      const query = searchParams.toString();
      window.location.href = query
        ? `${basePath}/dags/${newFileName}?${query}`
        : `${basePath}/dags/${newFileName}`;
    } catch {
      setRenameError('An unexpected error occurred');
      setIsRenameLoading(false);
    }
  };

  return (
    <div className="flex items-center gap-2">
      <Button onClick={() => setIsRenameModalOpen(true)}>
        <PencilLine className="h-4 w-4" />
        Rename
      </Button>

      <Button
        variant="destructive"
        onClick={async () => {
          if (!confirm('Are you sure to delete the DAG?')) {
            return;
          }
          const { error } = await client.DELETE('/dags/{fileName}', {
            params: {
              path: {
                fileName: fileName,
              },
              query: {
                remoteNode,
              },
            },
          });
          if (error) {
            showError(
              error.message || 'Failed to delete DAG',
              'Please try again or check the server connection.'
            );
            return;
          }
          // Redirect to the DAGs list page
          const basePath = window.location.pathname.split('/dags')[0] || '';
          const searchParams = new URLSearchParams();
          searchParams.set('remoteNode', remoteNode);
          const query = searchParams.toString();
          window.location.href = query
            ? `${basePath}/dags/?${query}`
            : `${basePath}/dags/`;
        }}
      >
        <Trash2 className="h-4 w-4" />
        Delete
      </Button>

      <DAGNameInputModal
        isOpen={isRenameModalOpen}
        onClose={handleRenameClose}
        onSubmit={handleRenameSubmit}
        mode="rename"
        initialValue={fileName}
        isLoading={isRenameLoading}
        externalError={renameError}
      />
    </div>
  );
}

export default DAGEditButtons;
