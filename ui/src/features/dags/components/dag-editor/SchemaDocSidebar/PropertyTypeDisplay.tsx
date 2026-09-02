import { cn } from '@/lib/utils';
import { I18nText } from '@/i18n/I18nText';

interface PropertyTypeDisplayProps {
  type: string | string[];
  required?: boolean;
  className?: string;
}

// Semantic color palette matching existing StatusChip patterns
const typeColors: Record<string, string> = {
  string: 'status-success',
  number: 'status-info',
  integer: 'status-info',
  boolean: 'status-info',
  array: 'status-warning',
  object: 'status-warning',
  enum: 'status-warning',
  null: 'status-muted',
  unknown: 'status-muted',
};

export function PropertyTypeDisplay({
  type,
  required,
  className,
}: PropertyTypeDisplayProps) {
  const types = Array.isArray(type) ? type : [type];

  return (
    <div className={cn('flex flex-wrap items-center gap-1', className)}>
      {types.map((t, i) => (
        <span
          key={i}
          className={cn(
            'inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium',
            typeColors[t] || typeColors.unknown
          )}
        >
          {t}
        </span>
      ))}
      {required && (
        <span className="inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium status-failed">
          <I18nText text={"required"} />
        </span>
      )}
    </div>
  );
}
