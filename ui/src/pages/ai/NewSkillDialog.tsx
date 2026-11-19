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
import { SKILL_NAME_MAX, skillNameProblem } from '@/features/skills/skill';
import { useI18n } from '@/i18n/I18nProvider';

/**
 * Asks for the name of a new skill. `onCreate` writes the file and returns
 * the message of an error, or null when the file exists.
 */
export function NewSkillDialog({
  existing,
  onCreate,
  onClose,
}: {
  /** The names in use. Undefined while the list loads. */
  existing: readonly string[] | undefined;
  onCreate(name: string): Promise<string | null>;
  onClose(): void;
}): ReactElement {
  const { ts } = useI18n();
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  const name = text.trim();
  const problem = skillNameProblem(name, existing);
  const message =
    problem === 'pattern'
      ? ts(
          'Use lowercase letters and digits. Join words with one -. Do not start or end with -.'
        )
      : problem === 'length'
        ? ts('Use at most {max} characters.', { max: SKILL_NAME_MAX })
        : problem === 'reserved'
          ? ts('"{name}" is reserved. Choose another name.', { name })
          : problem === 'exists'
            ? ts('A skill named "{name}" exists.', { name })
            : null;
  const canCreate =
    name !== '' && problem === null && existing !== undefined && !busy;

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!canCreate) return;
    setBusy(true);
    setFailure(null);
    const error = await onCreate(name);
    setBusy(false);
    if (error) setFailure(error);
  };

  return (
    <Dialog open onOpenChange={(open) => !open && !busy && onClose()}>
      <DialogContent className="sm:max-w-[425px]">
        <form onSubmit={(event) => void submit(event)}>
          <DialogHeader>
            <DialogTitle>{ts('New skill')}</DialogTitle>
            <DialogDescription>
              {ts(
                'The name of the folder. You cannot change it after you save.'
              )}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-1 py-4">
            <Label htmlFor="new-skill-name">{ts('Skill name')}</Label>
            <Input
              id="new-skill-name"
              value={text}
              autoFocus
              autoComplete="off"
              maxLength={SKILL_NAME_MAX + 1}
              spellCheck={false}
              disabled={busy}
              aria-invalid={message ? true : undefined}
              aria-describedby={message ? 'new-skill-problem' : undefined}
              className="font-mono"
              onChange={(event) => {
                setText(event.target.value);
                setFailure(null);
              }}
            />
            {message && (
              <p id="new-skill-problem" className="text-xs text-destructive">
                {message}
              </p>
            )}
            {failure && (
              <p role="alert" className="text-xs text-destructive">
                {failure}
              </p>
            )}
          </div>
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={onClose}>
              {ts('Cancel')}
            </Button>
            <Button type="submit" variant="primary" disabled={!canCreate}>
              {ts('Create')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
