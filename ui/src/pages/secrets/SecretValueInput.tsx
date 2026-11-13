// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Textarea } from '@/components/ui/textarea';
import { ToggleButton, ToggleGroup } from '@/components/ui/toggle-group';
import { I18nProps } from '@/i18n/I18nProps';
import { I18nTemplate } from '@/i18n/I18nTemplate';
import { I18nText } from '@/i18n/I18nText';
import React, { useEffect, useRef, useState } from 'react';

type ValueMode = 'single' | 'text' | 'file';

type LoadedFile = { name: string; size: number };

// SecretValueInput enters a secret value. It is a masked single-line input by
// default. "Multi-line or file" switches it to a text area or to a file picker
// that never shows the contents, so a value such as a PEM private key keeps
// its line breaks.
export function SecretValueInput({
  id,
  value,
  onChange,
}: {
  id: string;
  value: string;
  onChange: (value: string) => void;
}): React.ReactElement {
  const [mode, setMode] = useState<ValueMode>('single');
  const [loadedFile, setLoadedFile] = useState<LoadedFile | null>(null);
  const [readFailed, setReadFailed] = useState(false);
  // Counts file reads, so a read that finishes after a newer selection, a
  // mode change, or unmount is ignored.
  const readGeneration = useRef(0);

  useEffect(
    () => () => {
      readGeneration.current++;
    },
    []
  );

  function switchMode(next: ValueMode): void {
    if (next === mode) return;
    readGeneration.current++;
    setMode(next);
    setLoadedFile(null);
    setReadFailed(false);
    onChange('');
  }

  async function handleFile(
    event: React.ChangeEvent<HTMLInputElement>
  ): Promise<void> {
    const generation = ++readGeneration.current;
    setLoadedFile(null);
    setReadFailed(false);
    onChange('');
    const file = event.target.files?.[0];
    if (!file) return;
    try {
      const text = await file.text();
      if (generation !== readGeneration.current) return;
      onChange(text);
      setLoadedFile({ name: file.name, size: file.size });
    } catch {
      if (generation === readGeneration.current) setReadFailed(true);
    }
  }

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2">
        <Checkbox
          id={`${id}-multiline`}
          checked={mode !== 'single'}
          onCheckedChange={(checked) =>
            switchMode(checked === true ? 'text' : 'single')
          }
        />
        <Label htmlFor={`${id}-multiline`} className="font-normal">
          <I18nText text={'Multi-line or file'} />
        </Label>
      </div>

      {mode === 'single' ? (
        <Input
          id={id}
          value={value}
          onChange={(event) => onChange(event.target.value)}
          type="password"
          autoComplete="new-password"
          className="h-9"
        />
      ) : (
        <>
          <I18nProps>
            <ToggleGroup aria-label="Value source">
              <ToggleButton
                value="text"
                groupValue={mode}
                onClick={() => switchMode('text')}
              >
                <I18nText text={'Text'} />
              </ToggleButton>
              <ToggleButton
                value="file"
                groupValue={mode}
                onClick={() => switchMode('file')}
              >
                <I18nText text={'File'} />
              </ToggleButton>
            </ToggleGroup>
          </I18nProps>
          {mode === 'text' ? (
            <Textarea
              id={id}
              value={value}
              onChange={(event) => onChange(event.target.value)}
              rows={6}
              spellCheck={false}
              autoComplete="off"
              className="font-mono text-xs"
            />
          ) : (
            <>
              <Input
                id={id}
                type="file"
                onChange={(event) => void handleFile(event)}
                className="h-9"
              />
              {loadedFile ? (
                <p className="text-xs text-muted-foreground">
                  <I18nTemplate
                    text={'{name} loaded ({size} bytes)'}
                    values={{ name: loadedFile.name, size: loadedFile.size }}
                  />
                </p>
              ) : null}
              {readFailed ? (
                <p className="text-xs text-destructive">
                  <I18nText text={'Failed to read the selected file'} />
                </p>
              ) : null}
            </>
          )}
        </>
      )}
    </div>
  );
}
