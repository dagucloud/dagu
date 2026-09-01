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

export function LanguageSelector({
  compact = false,
  variant = 'login',
}: LanguageSelectorProps) {
  const { locale, setLocale, t } = useI18n();
  const language =
    locale === 'en' ? t('language.english') : t('language.chinese');

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
        <SelectItem value="en">{t('language.english')}</SelectItem>
        <SelectItem value="zh-CN">{t('language.chinese')}</SelectItem>
      </SelectContent>
    </Select>
  );
}
