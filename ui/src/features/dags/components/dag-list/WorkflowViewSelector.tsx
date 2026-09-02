// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  Bookmark,
  Check,
  ChevronDown,
  RotateCcw,
  Save,
  Settings2,
  Star,
  Trash2,
} from 'lucide-react';
import React from 'react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import type { WorkflowFilterView } from './workflowViews';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';

type Props = {
  views: WorkflowFilterView[];
  activeViewId: string | null;
  defaultViewId?: string;
  isAllView: boolean;
  isActiveViewEdited: boolean;
  canManageViews: boolean;
  error?: string | null;
  onSelectView: (viewId: string) => void;
  onShowAll: () => void;
  onResetView: () => void;
  onSaveView: (
    name: string,
    makeDefault: boolean,
    pinned: boolean
  ) => Promise<void>;
  onUpdateView: () => Promise<void>;
  onSetDefault: (viewId: string | undefined) => Promise<void>;
  onSetPinned: (viewId: string, pinned: boolean) => Promise<void>;
  onDeleteView: (viewId: string) => Promise<void>;
};

export function WorkflowViewSelector({
  views,
  activeViewId,
  defaultViewId,
  isAllView,
  isActiveViewEdited,
  canManageViews,
  error,
  onSelectView,
  onShowAll,
  onResetView,
  onSaveView,
  onUpdateView,
  onSetDefault,
  onSetPinned,
  onDeleteView,
}: Props): React.ReactElement {
  const [saveDialogOpen, setSaveDialogOpen] = React.useState(false);
  const [manageDialogOpen, setManageDialogOpen] = React.useState(false);
  const [viewName, setViewName] = React.useState('');
  const [makeDefault, setMakeDefault] = React.useState(false);
  const [pinToSidebar, setPinToSidebar] = React.useState(false);
  const [isMutating, setIsMutating] = React.useState(false);
  const [pendingDelete, setPendingDelete] =
    React.useState<WorkflowFilterView | null>(null);

  const activeView = views.find((view) => view.id === activeViewId);
  const isCustomView = !activeView && !isAllView;
  const selectedLabel =
    activeView?.name ?? (isCustomView ? 'Custom view' : 'All workflows');
  const normalizedName = viewName.trim();
  const duplicateName = views.some(
    (view) => view.name.toLowerCase() === normalizedName.toLowerCase()
  );
  const canSave =
    canManageViews &&
    normalizedName.length > 0 &&
    !duplicateName &&
    !isMutating;

  const openSaveDialog = () => {
    setViewName('');
    setMakeDefault(false);
    setPinToSidebar(false);
    setSaveDialogOpen(true);
  };

  const runMutation = async (action: () => Promise<void>): Promise<void> => {
    setIsMutating(true);
    try {
      await action();
    } finally {
      setIsMutating(false);
    }
  };

  const saveView = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!canSave) {
      return;
    }
    try {
      await runMutation(() =>
        onSaveView(normalizedName, makeDefault, pinToSidebar)
      );
      setSaveDialogOpen(false);
    } catch {
      // The page displays the server error alongside the workflow controls.
    }
  };

  const requestDelete = (view: WorkflowFilterView) => {
    setManageDialogOpen(false);
    setPendingDelete(view);
  };

  const dismissDelete = () => {
    setPendingDelete(null);
    setManageDialogOpen(true);
  };

  const confirmDelete = async () => {
    if (!pendingDelete) {
      return;
    }
    try {
      await runMutation(() => onDeleteView(pendingDelete.id));
      setPendingDelete(null);
      setManageDialogOpen(true);
    } catch {
      // Keep the confirmation open so the operation can be retried.
    }
  };

  return (
    <>
      <div className="flex min-w-0 items-center gap-2">
        <span className="text-xs font-medium text-muted-foreground">
          <I18nText text={'View'} />
        </span>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              type="button"
              variant="outline"
              className="h-9 min-w-[190px] max-w-[280px] justify-start px-3"
              aria-label={`Workflow view: ${selectedLabel}`}
            >
              {activeView?.pinned ? (
                <Star className="fill-current text-primary" />
              ) : (
                <Bookmark />
              )}
              <span className="min-w-0 flex-1 truncate text-left">
                {selectedLabel}
              </span>
              {activeViewId === defaultViewId && (
                <Badge variant="primary" className="hidden sm:inline-flex">
                  <I18nText text={'Default'} />
                </Badge>
              )}
              {isActiveViewEdited && (
                <Badge variant="secondary">
                  <I18nText text={'Edited'} />
                </Badge>
              )}
              <ChevronDown className="text-muted-foreground" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" className="w-[280px]">
            <DropdownMenuItem onSelect={onShowAll}>
              <span className="mr-2 flex size-4 items-center justify-center">
                {isAllView && <Check />}
              </span>
              <I18nText text={'All workflows'} />
            </DropdownMenuItem>

            {views.length > 0 && (
              <>
                <DropdownMenuSeparator />
                <DropdownMenuLabel className="text-[11px] uppercase tracking-wide text-muted-foreground">
                  <I18nText text={'Shared views'} />
                </DropdownMenuLabel>
                {views.map((view) => (
                  <DropdownMenuItem
                    key={view.id}
                    onSelect={() => onSelectView(view.id)}
                  >
                    <span className="mr-2 flex size-4 items-center justify-center">
                      {view.id === activeViewId && <Check />}
                    </span>
                    <span className="min-w-0 flex-1 whitespace-normal break-words">
                      {view.name}
                    </span>
                    {view.pinned && (
                      <Star className="fill-current text-primary" />
                    )}
                    {view.id === defaultViewId && (
                      <Badge variant="primary">
                        <I18nText text={'Default'} />
                      </Badge>
                    )}
                  </DropdownMenuItem>
                ))}
              </>
            )}

            {activeView && isActiveViewEdited && (
              <>
                <DropdownMenuSeparator />
                {canManageViews && (
                  <DropdownMenuItem
                    onSelect={() =>
                      void runMutation(onUpdateView).catch(() => undefined)
                    }
                    disabled={isMutating}
                  >
                    <Save className="mr-2" />
                    <I18nText
                      text="Update “{name}”"
                      values={{ name: activeView.name }}
                    />
                  </DropdownMenuItem>
                )}
                <DropdownMenuItem onSelect={onResetView}>
                  <RotateCcw className="mr-2" />
                  <I18nText text={'Reset changes'} />
                </DropdownMenuItem>
              </>
            )}

            {canManageViews && (
              <>
                <DropdownMenuSeparator />
                <DropdownMenuItem onSelect={openSaveDialog}>
                  <Save className="mr-2" />
                  <I18nText text={'Save current filters as view…'} />
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={() => setManageDialogOpen(true)}>
                  <Settings2 className="mr-2" />
                  <I18nText text={'Manage views…'} />
                </DropdownMenuItem>
              </>
            )}
          </DropdownMenuContent>
        </DropdownMenu>
      </div>

      <Dialog open={saveDialogOpen} onOpenChange={setSaveDialogOpen}>
        <DialogContent className="sm:max-w-[440px]">
          <form onSubmit={saveView}>
            <DialogHeader>
              <DialogTitle>
                <I18nText text={'Save workflow view'} />
              </DialogTitle>
              <DialogDescription>
                <I18nText
                  text={
                    'Save the current name and label filters, plus the sort order, for this remote and workspace.'
                  }
                />
              </DialogDescription>
            </DialogHeader>
            <div className="space-y-4 py-5">
              <div className="space-y-2">
                <Label htmlFor="workflow-view-name">
                  <I18nText text={'Name'} />
                </Label>
                <I18nProps>
                  <Input
                    id="workflow-view-name"
                    value={viewName}
                    maxLength={80}
                    autoFocus
                    placeholder="Production operations"
                    onChange={(event) => setViewName(event.target.value)}
                  />
                </I18nProps>
                {duplicateName && (
                  <p className="text-xs text-destructive">
                    <I18nText text={'A view with this name already exists.'} />
                  </p>
                )}
              </div>
              <div className="flex items-center gap-2">
                <Checkbox
                  id="workflow-view-pinned"
                  checked={pinToSidebar}
                  onCheckedChange={(checked) =>
                    setPinToSidebar(checked === true)
                  }
                />
                <Label htmlFor="workflow-view-pinned" className="font-normal">
                  <I18nText text={'Star and add to the sidebar for everyone'} />
                </Label>
              </div>
              <div className="flex items-center gap-2">
                <Checkbox
                  id="workflow-view-default"
                  checked={makeDefault}
                  onCheckedChange={(checked) =>
                    setMakeDefault(checked === true)
                  }
                />
                <Label htmlFor="workflow-view-default" className="font-normal">
                  <I18nText text={'Make this the default view for everyone'} />
                </Label>
              </div>
              {error && (
                <p role="alert" className="text-xs text-destructive">
                  {error}
                </p>
              )}
            </div>
            <DialogFooter>
              <Button
                type="button"
                variant="ghost"
                onClick={() => setSaveDialogOpen(false)}
              >
                <I18nText text={'Cancel'} />
              </Button>
              <Button type="submit" variant="primary" disabled={!canSave}>
                <I18nText text={'Save view'} />
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <Dialog open={manageDialogOpen} onOpenChange={setManageDialogOpen}>
        <DialogContent className="sm:max-w-[520px]">
          <DialogHeader>
            <DialogTitle>
              <I18nText text={'Manage workflow views'} />
            </DialogTitle>
            <DialogDescription>
              <I18nText
                text={
                  'Star shared sidebar shortcuts, choose the shared default, or remove views saved for this remote and workspace.'
                }
              />
            </DialogDescription>
          </DialogHeader>
          <div className="max-h-[360px] space-y-2 overflow-y-auto py-2">
            {views.length > 0 ? (
              views.map((view) => {
                const isDefault = view.id === defaultViewId;
                return (
                  <div
                    key={view.id}
                    className="flex items-center gap-2 rounded-md border border-border px-3 py-2"
                  >
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-sm"
                      aria-pressed={view.pinned}
                      aria-label={
                        view.pinned
                          ? `Remove ${view.name} from the sidebar`
                          : `Add ${view.name} to the sidebar`
                      }
                      disabled={isMutating}
                      onClick={() =>
                        void runMutation(() =>
                          onSetPinned(view.id, !view.pinned)
                        ).catch(() => undefined)
                      }
                    >
                      <Star
                        className={
                          view.pinned ? 'fill-current text-primary' : undefined
                        }
                      />
                    </Button>
                    <span className="min-w-0 flex-1 whitespace-normal break-words text-sm font-medium">
                      {view.name}
                    </span>
                    <Button
                      type="button"
                      variant={isDefault ? 'secondary' : 'ghost'}
                      size="sm"
                      aria-pressed={isDefault}
                      aria-label={
                        isDefault
                          ? `Remove ${view.name} as the default view`
                          : `Make ${view.name} the default view`
                      }
                      disabled={isMutating}
                      onClick={() =>
                        void runMutation(() =>
                          onSetDefault(isDefault ? undefined : view.id)
                        ).catch(() => undefined)
                      }
                    >
                      {isDefault ? (
                        <I18nText text={'Default'} />
                      ) : (
                        <I18nText text={'Make default'} />
                      )}
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-sm"
                      aria-label={`Delete ${view.name}`}
                      disabled={isMutating}
                      onClick={() => requestDelete(view)}
                    >
                      <Trash2 />
                    </Button>
                  </div>
                );
              })
            ) : (
              <div className="rounded-md border border-dashed border-border px-4 py-8 text-center text-sm text-muted-foreground">
                <I18nText text={'No saved workflow views yet.'} />
              </div>
            )}
          </div>
          {error && (
            <p role="alert" className="text-xs text-destructive">
              {error}
            </p>
          )}
          <DialogFooter>
            <Button type="button" onClick={() => setManageDialogOpen(false)}>
              <I18nText text={'Done'} />
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <I18nProps>
        <ConfirmDialog
          title="Delete workflow view?"
          buttonText="Delete view"
          visible={pendingDelete !== null}
          dismissModal={dismissDelete}
          onSubmit={confirmDelete}
        >
          <p className="text-sm text-muted-foreground">
            <I18nText
              text="“{name}” will be removed for everyone with access to this workspace scope. Workflows are not affected."
              values={{ name: pendingDelete?.name ?? '' }}
            />
          </p>
          {error && (
            <p role="alert" className="mt-2 text-xs text-destructive">
              {error}
            </p>
          )}
        </ConfirmDialog>
      </I18nProps>
    </>
  );
}
