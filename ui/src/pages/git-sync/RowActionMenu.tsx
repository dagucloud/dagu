// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { SyncStatus } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { MoreHorizontal, EyeOff, Trash2, ArrowRightLeft } from 'lucide-react';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';

interface RowActionMenuProps {
  itemId: string;
  status: SyncStatus;
  pushEnabled: boolean;
  canWrite: boolean;
  onForget: (itemId: string) => void;
  onDelete: (itemId: string) => void;
  onMove: (itemId: string) => void;
}

const canForget: Record<SyncStatus, boolean> = {
  [SyncStatus.synced]: false,
  [SyncStatus.modified]: false,
  [SyncStatus.untracked]: true,
  [SyncStatus.conflict]: true,
  [SyncStatus.missing]: true,
};

const canDelete: Record<SyncStatus, boolean> = {
  [SyncStatus.synced]: true,
  [SyncStatus.modified]: true,
  [SyncStatus.untracked]: false,
  [SyncStatus.conflict]: true,
  [SyncStatus.missing]: true,
};

const canMove: Record<SyncStatus, boolean> = {
  [SyncStatus.synced]: true,
  [SyncStatus.modified]: true,
  [SyncStatus.untracked]: false,
  [SyncStatus.conflict]: true,
  [SyncStatus.missing]: true,
};

export function RowActionMenu({
  itemId,
  status,
  pushEnabled,
  canWrite,
  onForget,
  onDelete,
  onMove,
}: RowActionMenuProps) {
  const { ts } = useI18n();
  if (!canWrite) return null;

  const showForget = canForget[status];
  const showDelete = canDelete[status];
  const showMove = canMove[status];

  if (!showForget && !showDelete && !showMove) return null;

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="sm"
          className="h-6 w-6 p-0"
          title={ts('More actions')}
        >
          <MoreHorizontal className="h-3 w-3" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-[140px]">
        {showForget && (
          <DropdownMenuItem onClick={() => onForget(itemId)}>
            <EyeOff className="h-3.5 w-3.5 mr-2" />
            <I18nText text={'Forget'} />
          </DropdownMenuItem>
        )}
        {showMove && (
          <I18nProps>
            <DropdownMenuItem
              onClick={() => onMove(itemId)}
              disabled={!pushEnabled}
              title={
                !pushEnabled ? 'Push disabled in read-only mode' : undefined
              }
            >
              <ArrowRightLeft className="h-3.5 w-3.5 mr-2" />
              <I18nText text={'Move'} />
            </DropdownMenuItem>
          </I18nProps>
        )}
        {showDelete && (
          <I18nProps>
            <DropdownMenuItem
              onClick={() => onDelete(itemId)}
              disabled={!pushEnabled}
              className="text-destructive focus:text-destructive"
              title={
                !pushEnabled ? 'Push disabled in read-only mode' : undefined
              }
            >
              <Trash2 className="h-3.5 w-3.5 mr-2" />
              <I18nText text={'Delete'} />
            </DropdownMenuItem>
          </I18nProps>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
