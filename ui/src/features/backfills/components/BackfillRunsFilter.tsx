// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { RunBackfillFilter } from '@/api/v1/schema';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { I18nProps } from '@/i18n/I18nProps';
import { I18nText } from '@/i18n/I18nText';
import { asBackfillFilter } from '@/features/dag-runs/lib/runViews';

type Props = {
  value: RunBackfillFilter;
  onChange: (value: RunBackfillFilter) => void;
  className?: string;
};

/** Picks whether a list shows every run, all but backfill runs, or only them. */
export function BackfillRunsFilter({ value, onChange, className }: Props) {
  return (
    <Select
      value={value}
      onValueChange={(next) => onChange(asBackfillFilter(next))}
    >
      <I18nProps>
        <SelectTrigger aria-label="Backfill runs" className={className}>
          <SelectValue />
        </SelectTrigger>
      </I18nProps>
      <SelectContent>
        <SelectItem value={RunBackfillFilter.all}>
          <I18nText text={'All runs'} />
        </SelectItem>
        <SelectItem value={RunBackfillFilter.exclude}>
          <I18nText text={'Without backfills'} />
        </SelectItem>
        <SelectItem value={RunBackfillFilter.only}>
          <I18nText text={'Backfill runs only'} />
        </SelectItem>
      </SelectContent>
    </Select>
  );
}
