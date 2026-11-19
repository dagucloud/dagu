// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  Bookmark,
  Check,
  ChevronDown,
  PencilLine,
  RotateCcw,
  Save,
  Settings2,
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
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';

export type SavedViewItem = {
  id: string;
  name: string;
  pinned: boolean;
};

type ViewKind = 'workflow' | 'run' | 'artifact';

const VIEW_COPY: Record<
  ViewKind,
  {
    triggerAria: (label: string) => string;
    allItems: string;
    saveTitle: string;
    saveDescription: string;
    manageTitle: string;
    manageDescription: string;
    deleteTitle: string;
    deleteBody: string;
    noViews: string;
  }
> = {
  workflow: {
    triggerAria: (label: string) => `Workflow view: ${label}`,
    allItems: 'All workflows',
    saveTitle: 'Save workflow view',
    saveDescription:
      'Save the current name and label filters, plus the sort order, for this remote and workspace.',
    manageTitle: 'Manage workflow views',
    manageDescription:
      'Choose the shared default, or remove views saved for this remote and workspace.',
    deleteTitle: 'Delete workflow view?',
    deleteBody:
      '“{name}” will be removed for everyone with access to this workspace scope. Workflows are not affected.',
    noViews: 'No saved workflow views yet.',
  },
  run: {
    triggerAria: (label: string) => `Run view: ${label}`,
    allItems: 'All runs',
    saveTitle: 'Save run view',
    saveDescription:
      'Save the current name, run ID, status, label, and date filters for this remote and workspace.',
    manageTitle: 'Manage run views',
    manageDescription:
      'Choose the shared default, or remove views saved for this remote and workspace.',
    deleteTitle: 'Delete run view?',
    deleteBody:
      '“{name}” will be removed for everyone with access to this workspace scope. Runs are not affected.',
    noViews: 'No saved run views yet.',
  },
  artifact: {
    triggerAria: (label: string) => `Artifact view: ${label}`,
    allItems: 'All artifacts',
    saveTitle: 'Save artifact view',
    saveDescription:
      'Save the current DAG name, file name, and date filters for this remote and workspace.',
    manageTitle: 'Manage artifact views',
    manageDescription:
      'Choose the shared default, or remove views saved for this remote and workspace.',
    deleteTitle: 'Delete artifact view?',
    deleteBody:
      '“{name}” will be removed for everyone with access to this workspace scope. Artifacts are not affected.',
    noViews: 'No saved artifact views yet.',
  },
};

type Props = {
  kind: ViewKind;
  views: SavedViewItem[];
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
  /**
   * Renames a saved view. With it, the selector shows Save, Rename and Delete
   * buttons for the active view.
   */
  onRenameView?: (viewId: string, name: string) => Promise<void>;
  /** The current filters are those of a saved view: there is nothing new to save. */
  filtersAlreadySaved?: boolean;
};

export function ViewSelector({
  kind,
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
  onDeleteView,
  onRenameView,
  filtersAlreadySaved = false,
}: Props): React.ReactElement {
  const [saveDialogOpen, setSaveDialogOpen] = React.useState(false);
  const [manageDialogOpen, setManageDialogOpen] = React.useState(false);
  const [viewName, setViewName] = React.useState('');
  const [makeDefault, setMakeDefault] = React.useState(false);
  const [isMutating, setIsMutating] = React.useState(false);
  const [pendingDelete, setPendingDelete] =
    React.useState<SavedViewItem | null>(null);
  const [deleteFromManage, setDeleteFromManage] = React.useState(true);
  const [renameOpen, setRenameOpen] = React.useState(false);
  const [renameName, setRenameName] = React.useState('');

  const { ts } = useI18n();
  const copy = VIEW_COPY[kind];
  const activeView = views.find((view) => view.id === activeViewId);
  const isCustomView = !activeView && !isAllView;
  const selectedLabel =
    activeView?.name ?? (isCustomView ? 'Custom view' : copy.allItems);
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
      await runMutation(() => onSaveView(normalizedName, makeDefault, false));
      setSaveDialogOpen(false);
    } catch {
      // The page displays the server error alongside the view controls.
    }
  };

  const requestDelete = (view: SavedViewItem, fromManage = true) => {
    setDeleteFromManage(fromManage);
    setManageDialogOpen(false);
    setPendingDelete(view);
  };

  const dismissDelete = () => {
    setPendingDelete(null);
    setManageDialogOpen(deleteFromManage);
  };

  const normalizedRename = renameName.trim();
  const renameTaken = views.some(
    (view) =>
      view.id !== activeView?.id &&
      view.name.toLowerCase() === normalizedRename.toLowerCase()
  );
  const canRename =
    !!activeView &&
    normalizedRename.length > 0 &&
    normalizedRename !== activeView.name &&
    !renameTaken &&
    !isMutating;

  const renameView = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!canRename || !activeView || !onRenameView) {
      return;
    }
    try {
      await runMutation(() => onRenameView(activeView.id, normalizedRename));
      setRenameOpen(false);
    } catch {
      // The page displays the server error alongside the view controls.
    }
  };

  const confirmDelete = async () => {
    if (!pendingDelete) {
      return;
    }
    try {
      await runMutation(() => onDeleteView(pendingDelete.id));
      setPendingDelete(null);
      setManageDialogOpen(deleteFromManage);
    } catch {
      // Keep the confirmation open so the operation can be retried.
    }
  };

  return (
    <>
      <div
        className={
          // With the toolbar, the selector is a child of the row of the page,
          // and the toolbar follows the selector in that row.
          onRenameView ? 'contents' : 'flex min-w-0 items-center gap-2'
        }
      >
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              type="button"
              variant="outline"
              className="h-9 min-w-[190px] max-w-[280px] justify-start px-3"
              aria-label={copy.triggerAria(selectedLabel)}
            >
              <Bookmark />
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
              <I18nText text={copy.allItems} />
            </DropdownMenuItem>

            {views.length > 0 && (
              <>
                <DropdownMenuSeparator />
                <DropdownMenuLabel className="text-[11px] text-muted-foreground">
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
                {canManageViews && !onRenameView && (
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

            {canManageViews && !onRenameView && (
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
        {onRenameView && canManageViews && (
          <div className="flex items-center gap-1">
            <Button
              type="button"
              variant="primary"
              size="xs"
              className="h-7 w-7 justify-center p-0"
              title={
                activeView
                  ? ts('Save the changes of “{name}”', {
                      name: activeView.name,
                    })
                  : ts('Save current filters as view…')
              }
              aria-label={ts('Save view')}
              disabled={
                isMutating ||
                (activeView
                  ? !isActiveViewEdited
                  : isAllView || filtersAlreadySaved)
              }
              onClick={() =>
                activeView
                  ? void runMutation(onUpdateView).catch(() => undefined)
                  : openSaveDialog()
              }
            >
              <Save aria-hidden="true" className="h-3.5 w-3.5 shrink-0" />
            </Button>
            <Button
              type="button"
              variant="outline"
              size="xs"
              className="h-7 w-7 justify-center p-0 text-destructive hover:bg-destructive/10 hover:text-destructive"
              title={ts('Delete view')}
              aria-label={ts('Delete view')}
              disabled={isMutating || !activeView}
              onClick={() => activeView && requestDelete(activeView, false)}
            >
              <Trash2 aria-hidden="true" className="h-3.5 w-3.5 shrink-0" />
            </Button>
            {activeView && (
              <>
                <Button
                  type="button"
                  variant="outline"
                  size="xs"
                  className="h-7 w-7 justify-center p-0"
                  title={ts('Rename view')}
                  aria-label={ts('Rename view')}
                  disabled={isMutating}
                  onClick={() => {
                    setRenameName(activeView.name);
                    setRenameOpen(true);
                  }}
                >
                  <PencilLine
                    aria-hidden="true"
                    className="h-3.5 w-3.5 shrink-0"
                  />
                </Button>
              </>
            )}
          </div>
        )}
      </div>

      <Dialog open={saveDialogOpen} onOpenChange={setSaveDialogOpen}>
        <DialogContent className="sm:max-w-[440px]">
          <form onSubmit={saveView}>
            <DialogHeader>
              <DialogTitle>
                <I18nText text={copy.saveTitle} />
              </DialogTitle>
              <DialogDescription>
                <I18nText text={copy.saveDescription} />
              </DialogDescription>
            </DialogHeader>
            <div className="space-y-4 py-5">
              <div className="space-y-2">
                <Label htmlFor={`view-name-${kind}`}>
                  <I18nText text={'Name'} />
                </Label>
                <I18nProps>
                  <Input
                    id={`view-name-${kind}`}
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
                  id={`view-default-${kind}`}
                  checked={makeDefault}
                  onCheckedChange={(checked) =>
                    setMakeDefault(checked === true)
                  }
                />
                <Label htmlFor={`view-default-${kind}`} className="font-normal">
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

      <Dialog open={renameOpen} onOpenChange={setRenameOpen}>
        <DialogContent className="sm:max-w-[440px]">
          <form onSubmit={renameView}>
            <DialogHeader>
              <DialogTitle>
                <I18nText text={'Rename view'} />
              </DialogTitle>
            </DialogHeader>
            <div className="space-y-2 py-5">
              <Label htmlFor={`view-rename-${kind}`}>
                <I18nText text={'Name'} />
              </Label>
              <Input
                id={`view-rename-${kind}`}
                value={renameName}
                maxLength={80}
                autoFocus
                onChange={(event) => setRenameName(event.target.value)}
              />
              {renameTaken && (
                <p className="text-xs text-destructive">
                  <I18nText text={'A view with this name already exists.'} />
                </p>
              )}
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
                onClick={() => setRenameOpen(false)}
              >
                <I18nText text={'Cancel'} />
              </Button>
              <Button type="submit" variant="primary" disabled={!canRename}>
                <I18nText text={'Rename'} />
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <Dialog open={manageDialogOpen} onOpenChange={setManageDialogOpen}>
        <DialogContent className="sm:max-w-[520px]">
          <DialogHeader>
            <DialogTitle>
              <I18nText text={copy.manageTitle} />
            </DialogTitle>
            <DialogDescription>
              <I18nText text={copy.manageDescription} />
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
                <I18nText text={copy.noViews} />
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
          title={copy.deleteTitle}
          buttonText="Delete view"
          visible={pendingDelete !== null}
          dismissModal={dismissDelete}
          onSubmit={confirmDelete}
        >
          <p className="text-sm text-muted-foreground">
            <I18nText
              text={copy.deleteBody}
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
