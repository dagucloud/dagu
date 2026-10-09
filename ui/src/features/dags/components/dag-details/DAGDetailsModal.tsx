import React from 'react';
import DAGDetailsSidePanel from './DAGDetailsSidePanel';
import { I18nTemplate } from '@/i18n/I18nTemplate';

type Props = {
  fileName: string;
  isOpen: boolean;
  onClose: () => void;
  /** Called after the user pins or unpins the DAG */
  onPinnedChange?: () => void;
};

function DAGDetailsModal({
  fileName,
  isOpen,
  onClose,
  onPinnedChange,
}: Props): React.ReactElement | null {
  return (
    <DAGDetailsSidePanel
      fileName={fileName}
      isOpen={isOpen}
      onClose={onClose}
      onPinnedChange={onPinnedChange}
      initialTab="status"
      toolbarHint={
        <I18nTemplate
          text="Use {up} {down} to navigate DAGs"
          values={{
            up: (
              <kbd className="px-1 py-0.5 bg-muted rounded text-xs font-mono">
                ↑
              </kbd>
            ),
            down: (
              <kbd className="px-1 py-0.5 bg-muted rounded text-xs font-mono">
                ↓
              </kbd>
            ),
          }}
        />
      }
    />
  );
}

export default DAGDetailsModal;
