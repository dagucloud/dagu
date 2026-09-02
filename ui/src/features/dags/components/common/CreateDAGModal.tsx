import { Button } from '@/components/ui/button';
import { useCanWrite } from '@/contexts/AuthContext';
import { Plus } from 'lucide-react';
import { useContext, useState } from 'react';
import { DAGNameInputModal } from '../../../../components/DAGNameInputModal';
import { AppBarContext } from '../../../../contexts/AppBarContext';
import { useClient } from '../../../../hooks/api';
import { defaultDAGSpec } from '../../../../lib/dagSpec';
import {
  isMutableWorkspaceSelection,
  sanitizeWorkspaceSelection,
  WorkspaceKind,
} from '../../../../lib/workspace';
import { I18nProps } from '@/i18n/I18nProps';
import { I18nText } from '@/i18n/I18nText';

/**
 * CreateDAGModal displays a button that opens a modal to create a new DAG
 * and redirects to the DAG specification page after creation
 */
function CreateDAGModal() {
  const appBarContext = useContext(AppBarContext);
  const canWrite = useCanWrite();
  const client = useClient();
  const [error, setError] = useState<string | null>(null);
  const [isOpen, setIsOpen] = useState(false);
  const [isLoading, setIsLoading] = useState(false);
  const workspaceSelection = sanitizeWorkspaceSelection(
    appBarContext.workspaceSelection
  );

  if (!canWrite || !isMutableWorkspaceSelection(workspaceSelection)) {
    return null;
  }

  const handleClose = () => {
    setIsOpen(false);
    setError(null);
  };

  const handleSubmit = async (name: string) => {
    setIsLoading(true);
    setError(null);

    try {
      const { error } = await client.POST('/dags', {
        params: {
          query: {
            remoteNode: appBarContext.selectedRemoteNode || 'local',
          },
        },
        body: {
          name,
          spec:
            workspaceSelection.kind === WorkspaceKind.workspace &&
            workspaceSelection.workspace
              ? defaultDAGSpec(workspaceSelection.workspace)
              : undefined,
        },
      });

      if (error) {
        setError(error.message || 'An error occurred');
        setIsLoading(false);
        return;
      }

      // Success - close modal and redirect
      setIsOpen(false);

      // Redirect to the DAG specification page
      const basePath = window.location.pathname.split('/dags')[0] || '';
      window.location.href = `${basePath}/dags/${name}/spec`;
    } catch {
      setError('An unexpected error occurred');
      setIsLoading(false);
    }
  };

  return (
    <>
      <I18nProps><Button aria-label="Create new DAG" onClick={() => setIsOpen(true)}>
        <Plus className="h-4 w-4" />
        <I18nText text={"New"} />
      </Button></I18nProps>

      <DAGNameInputModal
        isOpen={isOpen}
        onClose={handleClose}
        onSubmit={handleSubmit}
        mode="create"
        isLoading={isLoading}
        externalError={error}
      />
    </>
  );
}

export default CreateDAGModal;
