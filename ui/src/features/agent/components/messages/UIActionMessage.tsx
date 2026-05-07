import type React from 'react';
import { ExternalLink } from 'lucide-react';

import { Badge } from '@/components/ui/badge';

import { UIAction } from '../../types';

export function UIActionMessage({
  action,
}: {
  action?: UIAction;
}): React.ReactNode {
  if (!action || action.type !== 'navigate') {
    return null;
  }

  return (
    <div className="flex items-center gap-2 text-xs text-muted-foreground">
      <Badge variant="info" className="h-6">
        <ExternalLink className="h-3 w-3" aria-hidden="true" />
        Navigation
      </Badge>
      <span className="min-w-0 truncate">
        Navigating to {action.path ?? '(unknown path)'}
      </span>
    </div>
  );
}
