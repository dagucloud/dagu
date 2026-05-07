export function ErrorMessage({
  content,
}: {
  content: string;
}): React.ReactNode {
  return (
    <div className="flex pr-8">
      <div className="max-w-full border-l-2 border-destructive/50 py-1 pl-2.5 pr-2 text-xs leading-relaxed">
        <span className="whitespace-pre-wrap break-words font-mono text-[11px] text-foreground">
          {content}
        </span>
      </div>
    </div>
  );
}
