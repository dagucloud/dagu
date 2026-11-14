import { cn } from '@/lib/utils';
import * as React from 'react';

interface TabsProps extends React.HTMLAttributes<HTMLDivElement> {
  value: string;
  children: React.ReactNode;
}

function Tabs({ className, children, ...props }: Omit<TabsProps, 'value'>) {
  return (
    <div
      className={cn(
        'inline-flex items-center border-b border-border',
        className
      )}
      {...props}
    >
      {children}
    </div>
  );
}

interface TabProps extends React.HTMLAttributes<HTMLElement> {
  value: string;
  isActive?: boolean;
  asChild?: boolean;
}

function Tab({
  className,
  isActive,
  children,
  asChild = false,
  ...props
}: Omit<TabProps, 'value'>) {
  const classes = cn(
    'inline-flex items-center justify-center gap-1.5 whitespace-nowrap h-11 px-3 text-sm font-medium relative',
    'transition-colors duration-150 ease-in-out focus-visible:outline-none',
    'disabled:pointer-events-none disabled:opacity-50',
    'border-b-2',
    isActive
      ? 'text-foreground border-foreground'
      : 'text-muted-foreground hover:text-foreground border-transparent',
    className
  );

  if (asChild) {
    return (
      <span className={classes} role="button" tabIndex={0} {...props}>
        {children}
      </span>
    );
  }

  return (
    <button className={classes} {...props}>
      {children}
    </button>
  );
}

export { Tab, Tabs };
