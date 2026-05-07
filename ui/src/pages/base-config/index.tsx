import { Globe2, RotateCcw, Save, SquareStack } from 'lucide-react';
import React, { useCallback, useEffect, useRef } from 'react';
import { Button } from '@/components/ui/button';
import { useErrorModal } from '@/components/ui/error-modal';
import { useSimpleToast } from '@/components/ui/simple-toast';
import { Tab, Tabs } from '@/components/ui/tabs';
import { useCanWriteForWorkspace } from '@/contexts/AuthContext';
import DAGEditorWithDocs from '@/features/dags/components/dag-editor/DAGEditorWithDocs';
import { useClient, useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import { workspaceNameForSelection } from '@/lib/workspace';
import { AppBarContext } from '../../contexts/AppBarContext';
import { useConfig } from '../../contexts/ConfigContext';

type ConfigScope = 'global' | 'workspace';

function BaseConfigPage(): React.ReactNode {
  const appBarContext = React.useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const workspaceSelection = appBarContext.workspaceSelection;
  const selectedWorkspace = workspaceNameForSelection(workspaceSelection);
  const hasWorkspaceConfig = !!selectedWorkspace;
  const client = useClient();
  const config = useConfig();
  const canWriteWorkspace = useCanWriteForWorkspace(selectedWorkspace);
  const { showError } = useErrorModal();
  const { showToast } = useSimpleToast();

  const [activeScope, setActiveScope] = React.useState<ConfigScope>('global');
  const [globalValue, setGlobalValue] = React.useState<string | null>(null);
  const [workspaceValue, setWorkspaceValue] = React.useState<string | null>(
    null
  );
  const [globalDirty, setGlobalDirty] = React.useState(false);
  const [workspaceDirty, setWorkspaceDirty] = React.useState(false);
  const saveHandlerRef = useRef<(() => Promise<void>) | null>(null);

  useEffect(() => {
    appBarContext.setTitle('Base Config');
  }, [appBarContext]);

  const { data: globalData, mutate: mutateGlobal } = useQuery(
    '/settings/base-config',
    {
      params: {
        query: { remoteNode },
      },
    },
    {
      revalidateOnFocus: true,
      revalidateOnMount: true,
    }
  );

  const { data: workspaceData, mutate: mutateWorkspace } = useQuery(
    '/settings/workspaces/{workspaceName}/base-config',
    whenEnabled(hasWorkspaceConfig, {
      params: {
        path: { workspaceName: selectedWorkspace },
        query: { remoteNode },
      },
    }),
    {
      revalidateOnFocus: true,
      revalidateOnMount: true,
    }
  );

  useEffect(() => {
    if (globalData?.spec !== undefined && globalValue === null) {
      setGlobalValue(globalData.spec);
    }
  }, [globalData, globalValue]);

  useEffect(() => {
    if (workspaceData?.spec !== undefined && workspaceValue === null) {
      setWorkspaceValue(workspaceData.spec);
    }
  }, [workspaceData, workspaceValue]);

  useEffect(() => {
    if (!hasWorkspaceConfig && activeScope === 'workspace') {
      setActiveScope('global');
    }
  }, [activeScope, hasWorkspaceConfig]);

  useEffect(() => {
    setWorkspaceValue(null);
    setWorkspaceDirty(false);
  }, [selectedWorkspace, remoteNode]);

  useEffect(() => {
    setGlobalValue(null);
    setGlobalDirty(false);
  }, [remoteNode]);

  const globalEditable = !!config.permissions.writeDags;
  const workspaceEditable =
    !!config.permissions.writeDags && hasWorkspaceConfig && canWriteWorkspace;
  const editable =
    activeScope === 'workspace' ? workspaceEditable : globalEditable;
  const currentValue =
    activeScope === 'workspace' ? workspaceValue : globalValue;
  const currentSpec =
    activeScope === 'workspace' ? workspaceData?.spec : globalData?.spec;
  const hasUnsavedChanges =
    activeScope === 'workspace' ? workspaceDirty : globalDirty;
  const modelUri =
    activeScope === 'workspace'
      ? `inmemory://dagu/base-config.workspace.${selectedWorkspace}.yaml`
      : 'inmemory://dagu/base-config.global.yaml';
  const activeLabel =
    activeScope === 'workspace' ? `${selectedWorkspace} Workspace` : 'Global';

  const handleChange = useCallback(
    (newValue?: string) => {
      if (activeScope === 'workspace') {
        setWorkspaceValue(newValue || '');
        setWorkspaceDirty(true);
        return;
      }
      setGlobalValue(newValue || '');
      setGlobalDirty(true);
    },
    [activeScope]
  );

  const handleRevert = useCallback(() => {
    if (activeScope === 'workspace') {
      setWorkspaceValue(null);
      setWorkspaceDirty(false);
      mutateWorkspace();
      return;
    }
    setGlobalValue(null);
    setGlobalDirty(false);
    mutateGlobal();
  }, [activeScope, mutateGlobal, mutateWorkspace]);

  const handleSave = useCallback(async () => {
    if (currentValue === null) {
      return;
    }

    const request =
      activeScope === 'workspace'
        ? client.PUT('/settings/workspaces/{workspaceName}/base-config', {
            params: {
              path: { workspaceName: selectedWorkspace },
              query: { remoteNode },
            },
            body: {
              spec: currentValue,
            },
          })
        : client.PUT('/settings/base-config', {
            params: {
              query: { remoteNode },
            },
            body: {
              spec: currentValue,
            },
          });

    const { data: responseData, error } = await request;

    if (error) {
      showError(
        error.message || `Failed to save ${activeLabel.toLowerCase()} config`,
        'Please check the YAML syntax and try again.'
      );
      return;
    }

    if (responseData?.errors && responseData.errors.length > 0) {
      showError('Validation errors', responseData.errors.join('\n'));
      return;
    }

    if (activeScope === 'workspace') {
      setWorkspaceDirty(false);
      mutateWorkspace();
    } else {
      setGlobalDirty(false);
      mutateGlobal();
    }
    showToast(`${activeLabel} base config saved successfully`);
  }, [
    activeScope,
    activeLabel,
    currentValue,
    selectedWorkspace,
    remoteNode,
    client,
    showError,
    showToast,
    mutateGlobal,
    mutateWorkspace,
  ]);

  useEffect(() => {
    saveHandlerRef.current = handleSave;
  }, [handleSave]);

  useEffect(() => {
    if (!editable) {
      return;
    }

    const handleKeyDown = async (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key === 's') {
        event.preventDefault();
        if (saveHandlerRef.current) {
          await saveHandlerRef.current();
        }
      }
    };

    document.addEventListener('keydown', handleKeyDown);
    return () => document.removeEventListener('keydown', handleKeyDown);
  }, [editable]);

  return (
    <div className="flex flex-col flex-1 min-h-0 space-y-4 p-4">
      <div>
        <h1 className="text-lg font-semibold">Base Configuration</h1>
        <p className="text-sm text-muted-foreground">
          {activeScope === 'workspace'
            ? `${selectedWorkspace} overrides layered on top of global defaults`
            : 'Global defaults inherited by all DAG definitions'}
        </p>
      </div>

      {hasWorkspaceConfig ? (
        <Tabs role="tablist" aria-label="Base config scope" className="shrink-0">
          <Tab
            role="tab"
            aria-selected={activeScope === 'global'}
            isActive={activeScope === 'global'}
            onClick={() => setActiveScope('global')}
            className="gap-2 cursor-pointer"
          >
            <Globe2 className="h-4 w-4" />
            Global
          </Tab>
          <Tab
            role="tab"
            aria-selected={activeScope === 'workspace'}
            isActive={activeScope === 'workspace'}
            onClick={() => setActiveScope('workspace')}
            className="gap-2 cursor-pointer"
          >
            <SquareStack className="h-4 w-4" />
            Workspace
          </Tab>
        </Tabs>
      ) : null}

      <DAGEditorWithDocs
        value={
          editable ? (currentValue ?? currentSpec ?? '') : (currentSpec ?? '')
        }
        readOnly={!editable}
        onChange={editable ? handleChange : undefined}
        className="min-h-[400px]"
        modelUri={modelUri}
        headerActions={
          editable ? (
            <>
              <Button
                variant="outline"
                title="Revert to last saved version"
                disabled={!hasUnsavedChanges}
                onClick={handleRevert}
              >
                <RotateCcw className="h-4 w-4" />
                Revert
              </Button>
              <Button
                title="Save changes (Ctrl+S / Cmd+S)"
                disabled={!hasUnsavedChanges}
                onClick={handleSave}
              >
                <Save className="h-4 w-4" />
                Save
              </Button>
            </>
          ) : undefined
        }
      />
    </div>
  );
}

export default BaseConfigPage;
