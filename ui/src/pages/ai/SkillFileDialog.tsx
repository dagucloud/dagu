// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useState, type FormEvent, type ReactElement } from 'react';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  baseName,
  filePathProblem,
  formatBytes,
  SKILL_FILE_MAX_BYTES,
  type FilePathProblem,
} from '@/features/skills/skillFiles';
import { useI18n } from '@/i18n/I18nProvider';

export type FileDialogMode = 'add' | 'upload' | 'rename';

/**
 * Asks for the path of a file of a skill: a new text file, an uploaded file
 * (the user picks it here), or the new path of a file. `onSubmit` does the
 * write and gives the message of an error, or null when it worked.
 */
export function SkillFileDialog({
  mode,
  from,
  exists,
  onSubmit,
  onClose,
}: {
  mode: FileDialogMode;
  /** The path to rename, for the rename mode. */
  from?: string;
  /** The paths in use. */
  exists: readonly string[];
  onSubmit(path: string, file: File | null): Promise<string | null>;
  onClose(): void;
}): ReactElement {
  const { ts } = useI18n();
  const [path, setPath] = useState(
    mode === 'rename'
      ? (from ?? '')
      : mode === 'add'
        ? 'references/'
        : 'assets/'
  );
  const [file, setFile] = useState<File | null>(null);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  const typed = path.trim();
  const base = mode === 'rename' && typed === from ? 'exists' : null;
  const problem: FilePathProblem | null =
    typed === '' || typed.endsWith('/')
      ? null
      : (base ?? filePathProblem(typed, exists));
  const tooBig = file !== null && file.size > SKILL_FILE_MAX_BYTES;
  const messages: Record<FilePathProblem, string> = {
    empty: ts('Enter a path.'),
    'too-long': ts('The path is too long.'),
    syntax: ts(
      'Use / between folders. A part cannot be empty, . or .., and the path cannot start with /.'
    ),
    'skill-md': ts('SKILL.md is the file of the skill. Edit it in the editor.'),
    folder: ts('The path must start with references/, assets/ or scripts/.'),
    hidden: ts('A hidden file is not allowed. Only .gitkeep is.'),
    exists: ts('A file with this path exists.'),
  };
  const canSubmit =
    typed !== '' &&
    !typed.endsWith('/') &&
    problem === null &&
    !busy &&
    (mode !== 'upload' || (file !== null && !tooBig));

  const title =
    mode === 'add'
      ? ts('Add a file')
      : mode === 'upload'
        ? ts('Upload a file')
        : ts('Rename the file');

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!canSubmit) return;
    setBusy(true);
    setFailure(null);
    const error = await onSubmit(typed, file);
    setBusy(false);
    if (error) setFailure(error);
  };

  return (
    <Dialog open onOpenChange={(open) => !open && !busy && onClose()}>
      <DialogContent className="sm:max-w-[460px]">
        <form onSubmit={(event) => void submit(event)}>
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
            <DialogDescription>
              {mode === 'rename'
                ? ts(
                    'The server writes the new file, then deletes the old one.'
                  )
                : ts(
                    'A skill keeps its files in references/, assets/ and scripts/.'
                  )}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3 py-4">
            {mode === 'upload' && (
              <div className="space-y-1">
                <Label htmlFor="skill-upload-file">{ts('File')}</Label>
                <Input
                  id="skill-upload-file"
                  type="file"
                  disabled={busy}
                  className="h-auto py-1 text-xs"
                  onChange={(event) => {
                    const picked = event.target.files?.[0] ?? null;
                    setFile(picked);
                    setFailure(null);
                    if (picked) {
                      setPath((current) => {
                        const dir = current.includes('/')
                          ? current.slice(0, current.lastIndexOf('/') + 1)
                          : 'assets/';
                        return dir + baseName(picked.name);
                      });
                    }
                  }}
                />
                {file && (
                  <p
                    className={
                      tooBig
                        ? 'text-xs text-destructive'
                        : 'text-xs text-muted-foreground'
                    }
                  >
                    {tooBig
                      ? ts('The file has {size}. The limit is {limit}.', {
                          size: formatBytes(file.size),
                          limit: formatBytes(SKILL_FILE_MAX_BYTES),
                        })
                      : formatBytes(file.size)}
                  </p>
                )}
              </div>
            )}
            <div className="space-y-1">
              <Label htmlFor="skill-file-path">{ts('Path in the skill')}</Label>
              <Input
                id="skill-file-path"
                value={path}
                autoFocus={mode !== 'upload'}
                autoComplete="off"
                spellCheck={false}
                disabled={busy}
                aria-invalid={problem ? true : undefined}
                aria-describedby={problem ? 'skill-file-problem' : undefined}
                className="font-mono"
                placeholder="references/notes.md"
                onChange={(event) => {
                  setPath(event.target.value);
                  setFailure(null);
                }}
              />
              {problem && (
                <p id="skill-file-problem" className="text-xs text-destructive">
                  {messages[problem]}
                </p>
              )}
              {failure && (
                <p role="alert" className="text-xs text-destructive">
                  {failure}
                </p>
              )}
            </div>
          </div>
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={onClose}>
              {ts('Cancel')}
            </Button>
            <Button type="submit" variant="primary" disabled={!canSubmit}>
              {mode === 'rename'
                ? ts('Rename')
                : mode === 'upload'
                  ? ts('Upload')
                  : ts('Create')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
