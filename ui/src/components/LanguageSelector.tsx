import { Languages } from 'lucide-react';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';

type LanguageSelectorProps = {
  compact?: boolean;
  variant?: 'login' | 'sidebar';
};

const languages = [
  { locale: 'en', key: 'language.english' },
  { locale: 'zh-CN', key: 'language.chinese' },
  { locale: 'ja', key: 'language.japanese' },
] as const;

export function LanguageSelector({
  compact = false,
  variant = 'login',
}: LanguageSelectorProps) {
  const { locale, setLocale, t } = useI18n();
  const language = t(
    languages.find((item) => item.locale === locale)?.key ?? 'language.english'
  );

  return (
    <Select
      value={locale}
      onValueChange={(value) => setLocale(value as typeof locale)}
    >
      <SelectTrigger
        aria-label={t('language.select')}
        title={compact ? language : undefined}
        className={cn(
          'h-7 border-transparent bg-transparent px-2 py-1 text-xs shadow-none hover:border-transparent',
          variant === 'sidebar'
            ? 'w-full justify-start text-sidebar-foreground hover:bg-sidebar-hover focus-visible:border-sidebar-ring [&>svg:last-child]:ml-auto'
            : 'w-auto text-muted-foreground hover:bg-muted hover:text-foreground',
          compact && 'w-7 justify-center px-1 [&>svg:last-child]:hidden'
        )}
      >
        <Languages
          className={cn(
            'h-4 w-4 shrink-0',
            variant === 'sidebar' && 'text-sidebar-foreground'
          )}
        />
        {!compact && <SelectValue />}
      </SelectTrigger>
      <SelectContent>
        {languages.map((item) => (
          <SelectItem key={item.locale} value={item.locale}>
            {t(item.key)}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
