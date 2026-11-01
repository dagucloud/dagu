// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Files } from 'lucide-react';
import type { ReactElement } from 'react';
import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { useI18n } from '@/i18n/I18nProvider';

export interface DocumentChoice {
  value: string;
  label: string;
}

/**
 * The choice of the document of a multi-document YAML file. The canvas frame
 * is the `ve` container: from 40rem the choice is a select in the toolbar;
 * in a narrower frame it is an overflow button with the same choices, so
 * that it stays in reach.
 */
export function DocumentSwitcher({
  value,
  onChange,
  documents,
}: {
  value: string;
  onChange(value: string): void;
  documents: DocumentChoice[];
}): ReactElement {
  const { ts } = useI18n();
  return (
    <>
      <span className="inline-flex @max-[40rem]/ve:hidden">
        <Select value={value} onValueChange={onChange}>
          <SelectTrigger
            size="sm"
            className="h-7 min-w-[8rem] px-2 text-xs"
            aria-label={ts('Document')}
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {documents.map((document) => (
              <SelectItem key={document.value} value={document.value}>
                {document.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </span>
      <span className="hidden @max-[40rem]/ve:inline-flex">
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              type="button"
              variant="ghost"
              size="xs"
              title={ts('Switch document')}
              aria-label={ts('Switch document')}
            >
              <Files className="h-3.5 w-3.5" aria-hidden="true" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuRadioGroup value={value} onValueChange={onChange}>
              {documents.map((document) => (
                <DropdownMenuRadioItem
                  key={document.value}
                  value={document.value}
                >
                  {document.label}
                </DropdownMenuRadioItem>
              ))}
            </DropdownMenuRadioGroup>
          </DropdownMenuContent>
        </DropdownMenu>
      </span>
    </>
  );
}
