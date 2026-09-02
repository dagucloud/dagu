import type { YamlPathSegment } from '@/hooks/useYamlCursorPath';
import { cn } from '@/lib/utils';
import { ChevronRight } from 'lucide-react';
import { I18nText } from '@/i18n/I18nText';

interface SchemaPathBreadcrumbProps {
  segments: YamlPathSegment[];
  className?: string;
}

export function SchemaPathBreadcrumb({
  segments,
  className,
}: SchemaPathBreadcrumbProps) {
  if (segments.length === 0) {
    return (
      <div className={cn('text-xs text-muted-foreground italic', className)}>
        <I18nText text={"root"} />
      </div>
    );
  }

  return (
    <div
      className={cn(
        'flex items-center gap-0.5 text-xs text-muted-foreground overflow-x-auto',
        className
      )}
    >
      {segments.map((segment, index) => (
        <span key={index} className="flex items-center gap-0.5 shrink-0">
          {index > 0 && (
            <ChevronRight className="w-3 h-3 text-muted-foreground/50" />
          )}
          <span
            className={cn(
              'px-1 py-0.5 rounded text-foreground',
              segment.isArrayIndex ? 'font-mono status-warning' : 'bg-muted'
            )}
          >
            {segment.isArrayIndex ? `[${segment.key}]` : segment.key}
          </span>
        </span>
      ))}
    </div>
  );
}
