// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Monitor, Moon, Sun } from 'lucide-react';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  useUserPreferences,
  type ThemePreference,
} from '@/contexts/UserPreference';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';

const themes = [
  { value: 'light', key: 'theme.light', Icon: Sun },
  { value: 'dark', key: 'theme.dark', Icon: Moon },
  { value: 'system', key: 'theme.system', Icon: Monitor },
] as const;

type ThemeSelectorProps = {
  compact?: boolean;
};

/** Sidebar control for choosing the light, dark, or system theme. */
export function ThemeSelector({ compact = false }: ThemeSelectorProps) {
  const { preferences, updatePreference } = useUserPreferences();
  const { t } = useI18n();
  const current =
    themes.find((item) => item.value === preferences.theme) ?? themes[2];

  return (
    <Select
      value={current.value}
      onValueChange={(value) =>
        updatePreference('theme', value as ThemePreference)
      }
    >
      <SelectTrigger
        aria-label={t('theme.select')}
        title={compact ? t(current.key) : undefined}
        className={cn(
          'h-7 w-full justify-start border-transparent bg-transparent px-2 py-1 text-xs text-sidebar-foreground shadow-none hover:border-transparent hover:bg-sidebar-hover focus-visible:border-sidebar-ring [&>svg:last-child]:ml-auto',
          compact && 'w-7 justify-center px-1 [&>svg:last-child]:hidden'
        )}
      >
        <current.Icon className="h-4 w-4 shrink-0 text-sidebar-foreground" />
        {!compact && <SelectValue>{t(current.key)}</SelectValue>}
      </SelectTrigger>
      <SelectContent>
        {themes.map(({ value, key, Icon }) => (
          <SelectItem key={value} value={value}>
            <span className="flex items-center gap-2">
              <Icon className="h-3.5 w-3.5" aria-hidden="true" />
              {t(key)}
            </span>
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
