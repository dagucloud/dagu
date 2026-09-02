/**
 * ScriptDialog component for displaying script content in a clean modal.
 *
 * @module components/ui/script-dialog
 */
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { FileText } from 'lucide-react';
import { useState } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';

interface ScriptDialogProps {
  /** The script content to display */
  script: string;
  /** Optional step name for the dialog title */
  stepName?: string;
  /** Render prop for the trigger element */
  children: React.ReactNode;
}

/**
 * ScriptDialog displays script content in a clean modal dialog.
 * Click on the trigger element to open the dialog.
 */
export function ScriptDialog({
  script,
  stepName,
  children,
}: ScriptDialogProps) {
  const [open, setOpen] = useState(false);
  const { ts } = useI18n();

  const lines = script.split('\n');

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <div
        onClick={() => setOpen(true)}
        className="cursor-pointer"
        role="button"
        tabIndex={0}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault();
            setOpen(true);
          }
        }}
      >
        {children}
      </div>
      <DialogContent className="max-w-2xl max-h-[80vh] flex flex-col p-0 gap-0">
        <DialogHeader className="px-4 py-3 border-b border-border">
          <DialogTitle className="flex items-center gap-2 text-sm font-semibold">
            <FileText className="h-4 w-4 text-warning" />
            {stepName ? (
              ts('Script: {name}', { name: stepName })
            ) : (
              <I18nText text="Script Content" />
            )}
          </DialogTitle>
        </DialogHeader>
        <div className="flex-1 overflow-auto min-h-0 rounded-b-lg">
          <div className="min-h-full rounded-b-lg bg-slate-950">
            <pre className="font-mono text-xs text-slate-100 p-3 selection:bg-violet-500/40 selection:text-white">
              {lines.map((line, index) => (
                <div key={index} className="flex px-1 hover:bg-slate-900">
                  <span className="mr-4 w-8 flex-shrink-0 select-none text-right text-slate-500">
                    {index + 1}
                  </span>
                  <span className="whitespace-pre-wrap break-all flex-grow selection:bg-violet-500/40 selection:text-white">
                    {line || ' '}
                  </span>
                </div>
              ))}
            </pre>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}

interface ScriptBadgeProps {
  /** The script content */
  script: string;
  /** Optional step name for the dialog title */
  stepName?: string;
}

/**
 * ScriptBadge is a pre-styled trigger that shows "Script defined" badge.
 * Clicking it opens the ScriptDialog.
 */
export function ScriptBadge({ script, stepName }: ScriptBadgeProps) {
  return (
    <ScriptDialog script={script} stepName={stepName}>
      <div className="flex items-center gap-1.5 text-xs bg-warning-muted rounded-md px-1.5 py-0.5 w-fit hover:bg-warning-muted transition-colors">
        <FileText className="h-3.5 w-3.5 text-warning" />
        <span className="font-medium text-warning">
          <I18nText text={'Script defined'} />
        </span>
      </div>
    </ScriptDialog>
  );
}
