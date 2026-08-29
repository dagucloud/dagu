import ReactDiffViewer, { DiffMethod } from 'react-diff-viewer-continued';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { SyncStatus } from '@/api/v1/schema';
import { useUserPreferences } from '@/contexts/UserPreference';
import { Upload, RotateCcw, Trash2, EyeOff, RefreshCw, X } from 'lucide-react';
import { DialogClose } from '@/components/ui/dialog';

interface DiffModalProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  dagId: string;
  status?: SyncStatus;
  binary?: boolean;
  localSize?: number;
  remoteSize?: number;
  localContent?: string;
  remoteContent?: string;
  remoteCommit?: string;
  remoteAuthor?: string;
  remoteDeleted?: boolean;
  localExecutable?: boolean;
  remoteExecutable?: boolean;
  canPublish?: boolean;
  canRevert?: boolean;
  onPublish?: () => void;
  onRevert?: () => void;
  onForget?: () => void;
  onDelete?: () => void;
  isForgetting?: boolean;
  isDeleting?: boolean;
}

export function DiffModal({
  open,
  onOpenChange,
  dagId,
  status,
  binary,
  localSize,
  remoteSize,
  localContent,
  remoteContent,
  remoteCommit,
  remoteAuthor,
  remoteDeleted,
  localExecutable,
  remoteExecutable,
  canPublish,
  canRevert,
  onPublish,
  onRevert,
  onForget,
  onDelete,
  isForgetting,
  isDeleting,
}: DiffModalProps) {
  const { preferences } = useUserPreferences();
  const isDarkMode = preferences.theme === 'dark';

  const getTitles = () => {
    if (!status) return { left: 'Remote', right: 'Local' };

    switch (status) {
      case SyncStatus.modified:
        return {
          left: remoteCommit
            ? `Remote (${remoteCommit.slice(0, 7)})`
            : 'Remote',
          right: 'Local (modified)',
        };
      case SyncStatus.conflict:
        return {
          left: remoteDeleted
            ? 'Remote (deleted)'
            : remoteAuthor
              ? `Remote (${remoteAuthor})`
              : 'Remote',
          right: 'Local (conflicting)',
        };
      case SyncStatus.untracked:
        return {
          left: '(new file)',
          right: 'Local',
        };
      case SyncStatus.synced:
        return {
          left: 'Remote',
          right: 'Local (identical)',
        };
      case SyncStatus.missing:
        return {
          left: remoteCommit
            ? `Remote (${remoteCommit.slice(0, 7)})`
            : 'Remote',
          right: 'Local (missing)',
        };
      default:
        return { left: 'Remote', right: 'Local' };
    }
  };

  const titles = getTitles();

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        hideCloseButton
        className="max-w-6xl max-h-[90vh] overflow-hidden flex flex-col p-0 duration-100"
      >
        <DialogHeader className="px-4 py-3 border-b border-border/40 flex flex-row items-center justify-between space-y-0">
          <DialogTitle className="text-sm font-mono">{dagId}</DialogTitle>
          <DialogClose className="p-1.5 rounded-md opacity-70 transition-opacity hover:opacity-100 hover:bg-muted">
            <X className="h-4 w-4" />
            <span className="sr-only">Close</span>
          </DialogClose>
        </DialogHeader>
        <div className="flex-1 overflow-auto">
          {(remoteDeleted ||
            (localExecutable !== undefined &&
              remoteExecutable !== undefined)) && (
            <div className="border-b border-border/40 px-3 py-2 text-xs text-muted-foreground">
              {remoteDeleted
                ? 'The remote file was deleted.'
                : `Mode: remote ${remoteExecutable ? 'executable' : 'regular'}, local ${localExecutable ? 'executable' : 'regular'}`}
            </div>
          )}
          {binary ? (
            <div className="p-3 text-sm bg-muted/30">
              <div className="text-muted-foreground mb-3">
                Binary file. Content comparison is not available.
              </div>
              <table className="text-xs">
                <tbody>
                  <tr>
                    <td className="pr-4 py-0.5 text-muted-foreground">
                      {titles.left}
                    </td>
                    <td className="py-0.5 font-mono">
                      {remoteSize !== undefined
                        ? `${remoteSize.toLocaleString()} bytes`
                        : '—'}
                    </td>
                  </tr>
                  <tr>
                    <td className="pr-4 py-0.5 text-muted-foreground">
                      {titles.right}
                    </td>
                    <td className="py-0.5 font-mono">
                      {localSize !== undefined
                        ? `${localSize.toLocaleString()} bytes`
                        : '—'}
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          ) : (
            <ReactDiffViewer
              oldValue={remoteContent || ''}
              newValue={localContent || ''}
              splitView={true}
              leftTitle={titles.left}
              rightTitle={titles.right}
              useDarkTheme={isDarkMode}
              compareMethod={DiffMethod.LINES}
              showDiffOnly={false}
              styles={{
                variables: {
                  dark: {
                    diffViewerBackground: '#1e1e1e',
                    gutterBackground: '#252526',
                    addedBackground: '#1e3a29',
                    addedGutterBackground: '#1e3a29',
                    removedBackground: '#3a1e1e',
                    removedGutterBackground: '#3a1e1e',
                    wordAddedBackground: '#2ea043',
                    wordRemovedBackground: '#f85149',
                    emptyLineBackground: '#1e1e1e',
                    gutterColor: '#6e7681',
                  },
                  light: {
                    diffViewerBackground: '#ffffff',
                    gutterBackground: '#f6f8fa',
                    addedBackground: '#e6ffec',
                    addedGutterBackground: '#ccffd8',
                    removedBackground: '#ffebe9',
                    removedGutterBackground: '#ffd7d5',
                    wordAddedBackground: '#abf2bc',
                    wordRemovedBackground: '#ff818266',
                    emptyLineBackground: '#ffffff',
                    gutterColor: '#57606a',
                  },
                },
                contentText: {
                  fontFamily:
                    'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace',
                  fontSize: '12px',
                  lineHeight: '1.5',
                },
                titleBlock: {
                  padding: '8px 12px',
                  fontSize: '12px',
                  fontWeight: 500,
                },
                line: {
                  padding: '0 8px',
                },
              }}
            />
          )}
        </div>
        {status === SyncStatus.missing && (onForget || onDelete) ? (
          <DialogFooter className="px-4 py-3 border-t border-border/40">
            {onForget && (
              <Button
                variant="outline"
                size="sm"
                onClick={onForget}
                disabled={isForgetting}
              >
                {isForgetting ? (
                  <RefreshCw className="h-3.5 w-3.5 mr-1.5 animate-spin" />
                ) : (
                  <EyeOff className="h-3.5 w-3.5 mr-1.5" />
                )}
                Forget
              </Button>
            )}
            {onDelete && (
              <Button
                variant="destructive"
                size="sm"
                onClick={onDelete}
                disabled={isDeleting}
              >
                {isDeleting ? (
                  <RefreshCw className="h-3.5 w-3.5 mr-1.5 animate-spin" />
                ) : (
                  <Trash2 className="h-3.5 w-3.5 mr-1.5" />
                )}
                Delete from Remote
              </Button>
            )}
          </DialogFooter>
        ) : canPublish || canRevert ? (
          <DialogFooter className="px-4 py-3 border-t border-border/40">
            {canRevert && onRevert && (
              <Button
                variant="outline"
                size="sm"
                onClick={onRevert}
                className="text-destructive hover:text-destructive"
              >
                <RotateCcw className="h-3.5 w-3.5 mr-1.5" />
                Revert
              </Button>
            )}
            {canPublish && onPublish && (
              <Button size="sm" onClick={onPublish}>
                <Upload className="h-3.5 w-3.5 mr-1.5" />
                Push
              </Button>
            )}
          </DialogFooter>
        ) : null}
      </DialogContent>
    </Dialog>
  );
}
