// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * LogPageSizeSelect lets users pick how many log lines to load per view:
 * a preset size or a custom count.
 *
 * @module features/dags/components/dag-execution
 */
import { useState } from 'react';
import { Input } from '@/components/ui/input';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';

/**
 * Largest number of log lines that can be requested in one view. Matches the
 * log parameter maximum in api/v1/api.yaml.
 */
const MAX_LOG_PAGE_SIZE = 10000;

const LOG_PAGE_SIZE_OPTIONS = [100, 500, 1000, 5000, MAX_LOG_PAGE_SIZE];

const CUSTOM_OPTION = 'custom';

/**
 * Props for the LogPageSizeSelect component
 */
type Props = {
  /** Currently applied page size */
  pageSize: number;
  /** Called with the resolved line count (1..MAX_LOG_PAGE_SIZE) */
  onPageSizeChange: (pageSize: number) => void;
  disabled?: boolean;
};

function LogPageSizeSelect({ pageSize, onPageSizeChange, disabled }: Props) {
  const { ts } = useI18n();
  const [customSelected, setCustomSelected] = useState(
    !LOG_PAGE_SIZE_OPTIONS.includes(pageSize)
  );
  const [customValue, setCustomValue] = useState(String(pageSize));

  function commitCustomValue(): void {
    const parsed = Number(customValue);
    if (customValue.trim() === '' || !Number.isFinite(parsed)) {
      return;
    }
    const size = Math.min(Math.max(Math.floor(parsed), 1), MAX_LOG_PAGE_SIZE);
    setCustomValue(String(size));
    if (size !== pageSize) {
      onPageSizeChange(size);
    }
  }

  const selectValue = customSelected ? CUSTOM_OPTION : String(pageSize);

  return (
    <>
      <select
        aria-label={ts('Lines per page')}
        className="h-7 flex-shrink-0 rounded-md border border-border bg-surface px-2 text-xs text-foreground focus:border-ring focus:outline-none"
        value={selectValue}
        onChange={(e) => {
          const value = e.target.value;
          if (value === CUSTOM_OPTION) {
            setCustomSelected(true);
            setCustomValue(String(pageSize));
            return;
          }
          setCustomSelected(false);
          const size = Number(value);
          if (size !== pageSize) {
            onPageSizeChange(size);
          }
        }}
        disabled={disabled}
      >
        {LOG_PAGE_SIZE_OPTIONS.map((size) => (
          <option key={size} value={String(size)}>
            <I18nText text={`${size} lines`} />
          </option>
        ))}
        <option value={CUSTOM_OPTION}>
          <I18nText text={'Custom...'} />
        </option>
      </select>
      {customSelected && (
        <Input
          aria-label={ts('Custom lines per page')}
          type="number"
          min={1}
          max={MAX_LOG_PAGE_SIZE}
          value={customValue}
          onChange={(e) => setCustomValue(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              commitCustomValue();
            }
          }}
          onBlur={commitCustomValue}
          className="h-7 w-24 text-xs"
          disabled={disabled}
        />
      )}
    </>
  );
}

export default LogPageSizeSelect;
