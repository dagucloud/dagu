import React, { useContext } from 'react';
import { useClient } from '@/hooks/api';
import { AppBarContext } from '@/contexts/AppBarContext';
import DAGDetailsSidePanel from '@/features/dags/components/dag-details/DAGDetailsSidePanel';

interface DAGPreviewModalProps {
  fileName: string;
  isOpen: boolean;
  selectedWorkspace: string;
  onClose: () => void;
}

export function DAGPreviewModal({
  fileName,
  isOpen,
  selectedWorkspace,
  onClose,
}: DAGPreviewModalProps): React.ReactElement | null {
  const client = useClient();
  const appBarContext = useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';

  const handleEnqueue = React.useCallback(
    async (
      params: string,
      dagRunId?: string,
      _immediate?: boolean,
      profile?: string
    ): Promise<string | void> => {
      const labels: string[] = [];
      if (selectedWorkspace) {
        const safeName = selectedWorkspace.replace(/[^a-zA-Z0-9_-]/g, '');
        if (safeName) {
          labels.push(`workspace=${safeName}`);
        }
      }

      const { data, error } = await client.POST('/dags/{fileName}/enqueue', {
        params: {
          path: { fileName },
          query: { remoteNode },
        },
        body: {
          params: params || undefined,
          dagRunId: dagRunId || undefined,
          profile,
          labels: labels.length > 0 ? labels : undefined,
        },
      });

      if (error) {
        throw new Error(error.message || 'Failed to enqueue DAG execution.');
      }

      return data?.dagRunId;
    },
    [client, fileName, remoteNode, selectedWorkspace]
  );

  const toolbarHint = selectedWorkspace ? (
    <>
      Workspace:{' '}
      <span className="font-medium text-foreground">{selectedWorkspace}</span>
    </>
  ) : (
    'Template details'
  );

  return (
    <DAGDetailsSidePanel
      fileName={fileName}
      isOpen={isOpen}
      onClose={onClose}
      initialTab="status"
      toolbarHint={toolbarHint}
      renderInPortal={true}
      forceEnqueue={true}
      onEnqueue={handleEnqueue}
    />
  );
}
