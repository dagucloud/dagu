import React, { useContext } from 'react';
import { AppBarContext } from '@/contexts/AppBarContext';
import { usePeekOrOpen } from '@/features/peek/usePeekOrOpen';
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
  const peekOrOpen = usePeekOrOpen(remoteNode);

  // A template opens on its workflow page over the cockpit. The templates
  // listed are those of the selected workspace, whose label their runs keep.
  const openTemplate = (fileName: string) => {
    if (!fileName) {
      return;
    }
    peekOrOpen({ dag: fileName });
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
