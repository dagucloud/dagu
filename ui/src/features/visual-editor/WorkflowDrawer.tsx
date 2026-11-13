// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactElement, ReactNode } from 'react';
import { createPortal } from 'react-dom';
import {
  PANEL_BODY_CLASS,
  PanelHeader,
  usePanelColumn,
} from '@/features/dags/components/step-details/PanelShell';
import { cn } from '@/lib/utils';
import { StepSideDrawer } from '@/features/dags/components/step-details/StepSideDrawer';
import { useI18n } from '@/i18n/I18nProvider';
import {
  PanelRowTitle,
  ROW_PANEL_HEADER_CLASS,
  usePanelInToolbarRow,
} from './canvas/toolbarHost';
import { PanelSwitchButton } from './PanelSwitchButton';

/** The side drawer of the workflow panel: what the workflow is, apart from one step. */
export function WorkflowDrawer({
  open,
  onClose,
  bare = false,
  onShowYaml,
  onRestore,
  children,
}: {
  open: boolean;
  onClose(): void;
  /** The content brings its own body (tabs): no wrapper. */
  bare?: boolean;
  /** Swaps the panel for the YAML panel, from the header. */
  onShowYaml?(): void;
  /** Opens the panel again when a transient panel that covered it closes. */
  onRestore?(): void;
  children: ReactNode;
}): ReactElement {
  const { ts } = useI18n();
  const column = usePanelColumn();
  // The Workflow toggle of the row names the panel: the bar goes from md.
  const inRow = usePanelInToolbarRow();
  // In the right column of the host the panel is the body of its tab: the
  // column has the title and the button that collapses it.
  if (column) {
    if (!open || !column.workflow) return <></>;
    return createPortal(
      <div data-testid="ve-workflow" className="flex min-h-0 flex-1 flex-col">
        {bare ? (
          children
        ) : (
          <div className={cn(PANEL_BODY_CLASS, 'space-y-6')}>{children}</div>
        )}
      </div>,
      column.workflow
    );
  }
  return (
    <StepSideDrawer
      isOpen={open}
      onClose={onClose}
      hasContent={open}
      modal={false}
      label={ts('Workflow')}
      panelId="workflow"
      onRestore={onRestore}
    >
      <div data-testid="ve-workflow" className="flex min-h-0 flex-1 flex-col">
        <PanelRowTitle
          title={ts('Workflow')}
          closeLabel={ts('Close')}
          onClose={onClose}
        />
        <PanelHeader
          title={ts('Workflow')}
          closeLabel={ts('Close')}
          onClose={onClose}
          className={inRow ? ROW_PANEL_HEADER_CLASS : undefined}
        >
          {onShowYaml && (
            <div className="flex min-w-0 flex-1 items-center gap-2">
              <h2 className="truncate text-base font-semibold text-foreground">
                {ts('Workflow')}
              </h2>
              <PanelSwitchButton to="yaml" onClick={onShowYaml} />
            </div>
          )}
        </PanelHeader>
        {bare ? (
          children
        ) : (
          <div className={cn(PANEL_BODY_CLASS, 'space-y-6')}>{children}</div>
        )}
      </div>
    </StepSideDrawer>
  );
}
