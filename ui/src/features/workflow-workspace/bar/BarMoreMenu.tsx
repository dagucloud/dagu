// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  Copy,
  Link2,
  MoreHorizontal,
  PanelRight,
  Pin,
  RefreshCw,
  Webhook,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { useErrorModal } from '@/components/ui/error-modal';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { setDAGPinned } from '@/features/dags/lib/dagPins';
import { useClient } from '@/hooks/api';
import { useI18n } from '@/i18n/I18nProvider';
import { copyText } from '@/lib/clipboard';
import { cn } from '@/lib/utils';
import { useWorkspaceEditor } from '../canvas/WorkspaceEditorContext';
import type { BarSlotProps } from '../contracts';
import { useWorkspaceRefresh } from '../data';
import { useWorkspacePermissions } from '../data/useWorkspacePermissions';
import { useWorkspaceUrl } from '../state/workspaceUrl';
import { Z } from '../zLayers';
import { usePinChanged } from './DAGIdentity';
import { useWorkflowShare } from './useWorkflowShare';

const menuIcon = 'size-4 shrink-0';

/**
 * The controls that a bar under 672px hides, in one menu: the copy of the
 * name and of the link, the pin, Refresh, the Workflow panel with the labels
 * and the description, and the webhook. A wider bar shows none of it.
 */
export function BarMoreMenu(props: BarSlotProps & { pinned?: boolean }) {
  const { ts } = useI18n();
  return (
    <div data-testid="ww-bar-more" className="flex shrink-0 @2xl:hidden">
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label={ts('More actions')}
            title={ts('More actions')}
          >
            <MoreHorizontal aria-hidden="true" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className={cn(Z.menu, 'w-56')}>
          <MoreMenuItems {...props} />
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
}

/** The items of the menu; they mount, and read what they need, when it opens. */
function MoreMenuItems({
  fileName,
  dag,
  pinned,
}: BarSlotProps & { pinned?: boolean }) {
  const { ts } = useI18n();
  const client = useClient();
  const remoteNode = useRemoteNode();
  const { showError } = useErrorModal();
  const { canWrite: canPin, canManageWebhooks } = useWorkspacePermissions();
  const { handle } = useWorkspaceEditor();
  const [, setUrl] = useWorkspaceUrl();
  const refresh = useWorkspaceRefresh();
  const onPinChanged = usePinChanged();
  const { getPageLink } = useWorkflowShare(fileName);
  const name = dag.name;

  const togglePin = async () => {
    try {
      await setDAGPinned(client, { fileName, pinned: !pinned, remoteNode });
      onPinChanged();
    } catch (error) {
      showError(
        error instanceof Error ? error.message : String(error),
        ts('Please try again or check the server connection.')
      );
    }
  };

  return (
    <>
      <DropdownMenuItem className="gap-2" onSelect={() => void copyText(name)}>
        <Copy aria-hidden="true" className={menuIcon} />
        {ts('Copy name')}
      </DropdownMenuItem>
      <DropdownMenuItem
        className="gap-2"
        onSelect={() => void copyText(getPageLink())}
      >
        <Link2 aria-hidden="true" className={menuIcon} />
        {ts('Copy link to this workflow')}
      </DropdownMenuItem>
      {pinned !== undefined && canPin && (
        <DropdownMenuItem className="gap-2" onSelect={() => void togglePin()}>
          <Pin aria-hidden="true" className={menuIcon} />
          {ts(pinned ? 'Unpin workflow' : 'Pin workflow')}
        </DropdownMenuItem>
      )}
      <DropdownMenuItem
        className="gap-2"
        onSelect={() => void refresh().catch(() => undefined)}
      >
        <RefreshCw aria-hidden="true" className={menuIcon} />
        {ts('Refresh')}
      </DropdownMenuItem>
      <DropdownMenuItem
        className="gap-2"
        disabled={!handle}
        onSelect={() => handle?.openWorkflowPanel()}
      >
        <PanelRight aria-hidden="true" className={menuIcon} />
        {ts('Workflow panel')}
      </DropdownMenuItem>
      {canManageWebhooks && (
        <DropdownMenuItem
          className="gap-2"
          onSelect={() => setUrl({ panel: 'webhook' })}
        >
          <Webhook aria-hidden="true" className={menuIcon} />
          {ts('Open {tab}', { tab: ts('Webhook') })}
        </DropdownMenuItem>
      )}
    </>
  );
}
