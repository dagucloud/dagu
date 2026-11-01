import { cn } from '@/lib/utils';

/**
 * A spinner. With `fill` it takes the whole area of its parent and sits in the
 * middle of it, so that it does not jump from the top to the centre when the
 * page around it loads.
 */
function LoadingIndicator({ fill = false }: { fill?: boolean }) {
  return (
    <div
      className={cn(
        'w-full text-center mx-auto flex items-center justify-center p-4',
        fill && 'h-full min-h-48 flex-1'
      )}
    >
      <div className="h-8 w-8 animate-spin rounded-full border-4 border-muted-foreground border-t-transparent"></div>
    </div>
  );
}

export default LoadingIndicator;
