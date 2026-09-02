// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * DAGEditButtons component provides buttons for renaming and deleting a DAG.
 *
 * @module features/dags/components/dag-editor
 */
import { useCanWriteForWorkspace } from '@/contexts/AuthContext';
import { Button } from '@/components/ui/button';
import ConfirmModal from '@/components/ui/confirm-dialog';
import { useErrorModal } from '@/components/ui/error-modal';
import { PencilLine, Trash2 } from 'lucide-react';
import React from 'react';
import { DAGNameInputModal } from '../../../../components/DAGNameInputModal';
import { useRemoteNode } from '../../../../contexts/RemoteNodeContext';
import { useClient } from '../../../../hooks/api';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';

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
  const [isDeleteModalOpen, setIsDeleteModalOpen] = React.useState(false);
  const [isDeleteLoading, setIsDeleteLoading] = React.useState(false);
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
        <I18nText text={"Rename"} />
      </Button>

      <Button variant="destructive" onClick={() => setIsDeleteModalOpen(true)}>
        <Trash2 className="h-4 w-4" />
        <I18nText text={"Delete"} />
      </Button>

      <I18nProps><ConfirmModal
        title="Delete DAG"
        buttonText="Delete"
        visible={isDeleteModalOpen}
        dismissModal={() => {
          if (!isDeleteLoading) setIsDeleteModalOpen(false);
        }}
        submitDisabled={isDeleteLoading}
        onSubmit={async () => {
          if (isDeleteLoading) return;
          setIsDeleteLoading(true);
          try {
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

            setIsDeleteModalOpen(false);
            const basePath = window.location.pathname.split('/dags')[0] || '';
            const searchParams = new URLSearchParams();
            searchParams.set('remoteNode', remoteNode);
            const query = searchParams.toString();
            window.location.href = query
              ? `${basePath}/dags/?${query}`
              : `${basePath}/dags/`;
          } catch {
            showError(
              'Failed to delete DAG',
              'Please try again or check the server connection.'
            );
          } finally {
            setIsDeleteLoading(false);
          }
        }}
      >
        <div className="space-y-2 text-sm">
          <p><I18nText text={"Do you really want to delete this DAG?"} /></p>
          <p className="font-mono text-xs">{fileName}</p>
          <p className="text-muted-foreground">
            <I18nText text={"The definition file is removed; past run history is kept. This action cannot be undone."} />
          </p>
        </div>
      </ConfirmModal></I18nProps>

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
