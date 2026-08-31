import { Languages } from 'lucide-react';
import { useI18n } from '@/i18n/I18nProvider';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';

export function LanguageSelector({ compact = false }: { compact?: boolean }) {
  const { locale, setLocale, t } = useI18n();
  return (
    <Select
      value={locale}
      onValueChange={(value) => setLocale(value as typeof locale)}
    >
      <SelectTrigger
        aria-label={t('language.select')}
        className={
          compact
            ? 'h-7 w-7 border-transparent px-1 py-1 [&>svg:last-child]:hidden'
            : 'h-7 w-full py-1'
        }
      >
        <Languages className="h-4 w-4 shrink-0" />
        {!compact && <SelectValue />}
      </SelectTrigger>
      <SelectContent>
        <SelectItem value="en">{t('language.english')}</SelectItem>
        <SelectItem value="zh-CN">{t('language.chinese')}</SelectItem>
      </SelectContent>
    </Select>
  );
}
