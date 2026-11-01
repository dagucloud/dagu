import React, { useEffect, useState } from 'react';
import { useCockpitState } from '@/features/cockpit/hooks/useCockpitState';
import { CockpitToolbar } from '@/features/cockpit/components/CockpitToolbar';
import { DateKanbanList } from '@/features/cockpit/components/DateKanbanList';
import { AppBarContext } from '@/contexts/AppBarContext';

export default function CockpitPage(): React.ReactElement {
  const { setTitle } = React.useContext(AppBarContext);
  const [isTemplateSelectorOpen, setIsTemplateSelectorOpen] = useState(false);
  const { selectedWorkspace, workspaceKey } = useCockpitState();

  useEffect(() => {
    setTitle('Cockpit');
  }, [setTitle]);

  const suspendBackgroundLoading = isTemplateSelectorOpen;

  return (
    <div className="flex flex-col h-full min-h-0">
      <CockpitToolbar
        selectedWorkspace={selectedWorkspace}
        onTemplateSelectorOpenChange={setIsTemplateSelectorOpen}
      />
      <DateKanbanList
        selectedWorkspace={selectedWorkspace}
        workspaceKey={workspaceKey}
        suspendLoadMore={suspendBackgroundLoading}
      />
    </div>
  );
}
