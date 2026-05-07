import type React from 'react';
import { XCircle } from 'lucide-react';

import { Alert, AlertDescription } from '@/components/ui/alert';

export function ErrorMessage({
  content,
}: {
  content: string;
}): React.ReactNode {
  return (
    <Alert
      variant="destructive"
      className="px-3 py-2 text-sm [&>svg]:left-3 [&>svg]:top-2.5 [&>svg~*]:pl-6"
    >
      <XCircle className="h-4 w-4" aria-hidden="true" />
      <AlertDescription className="whitespace-pre-wrap break-words text-xs">
        {content}
      </AlertDescription>
    </Alert>
  );
}
