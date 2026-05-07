import { cn } from '@/lib/utils';

export function UserMessage({
  content,
  isPending,
}: {
  content: string;
  isPending?: boolean;
}): React.ReactNode {
  if (!content) return null;

  return (
    <div className="flex justify-end pl-10">
      <div
        className={cn(
          'max-w-[85%] rounded-md border border-primary/20 bg-primary px-3 py-2 text-sm text-primary-foreground shadow-sm',
          isPending && 'opacity-60'
        )}
      >
        <p className="whitespace-pre-wrap break-words">{content}</p>
      </div>
    </div>
  );
}
