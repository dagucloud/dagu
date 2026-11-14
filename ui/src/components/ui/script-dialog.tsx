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
        className="min-w-0 cursor-pointer"
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
 * ScriptBadge previews the first line of a script and opens the full script
 * in a ScriptDialog when clicked.
 */
export function ScriptBadge({ script, stepName }: ScriptBadgeProps) {
  const { ts } = useI18n();
  const lines = script
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean);
  const moreLines = Math.max(0, lines.length - 1);

  return (
    <ScriptDialog script={script} stepName={stepName}>
      <div
        className="flex min-w-0 max-w-full items-center gap-1.5 text-xs font-medium"
        title={ts('View script')}
      >
        <FileText className="h-4 w-4 shrink-0 text-primary" />
        <span className="min-w-0 truncate whitespace-pre rounded-md bg-muted px-1.5 py-0.5 font-mono text-foreground/90 hover:bg-accent">
          {lines[0] ?? ''}
        </span>
        {moreLines > 0 && (
          <span className="shrink-0 text-muted-foreground">
            {ts(moreLines === 1 ? '+{count} line' : '+{count} lines', {
              count: moreLines,
            })}
          </span>
        )}
      </div>
    </ScriptDialog>
  );
}
