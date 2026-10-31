import React, { useContext } from 'react';
import { AppBarContext } from '@/contexts/AppBarContext';
import { buildDAGPageURL } from '@/features/dag-runs/lib/dagRunUrls';
import { useOpenPage } from '@/features/dag-runs/lib/useOpenPage';
import { TemplateSelector } from './TemplateSelector';

interface Props {
  selectedWorkspace: string;
  onTemplateSelectorOpenChange?: (isOpen: boolean) => void;
}

export function CockpitToolbar({
  selectedWorkspace,
  onTemplateSelectorOpenChange,
}: Props): React.ReactElement {
  const appBarContext = useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const openPage = useOpenPage();

  // A template opens on its workflow page, in the selected workspace.
  const openTemplate = (fileName: string) => {
    if (!fileName) {
      return;
    }
    openPage(
      buildDAGPageURL({
        fileName,
        remoteNode,
        workspace: selectedWorkspace || undefined,
      })
    );
  };

  return (
    <div className="flex items-center gap-3 flex-wrap mb-2 max-md:flex-col max-md:items-stretch">
      <TemplateSelector
        selectedWorkspace={selectedWorkspace}
        onSelect={openTemplate}
        onOpenChange={onTemplateSelectorOpenChange}
      />
    </div>
  );
}
