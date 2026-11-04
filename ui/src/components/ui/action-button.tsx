import type { VariantProps } from 'class-variance-authority';
import React, { ReactElement, forwardRef } from 'react';

import { Button, buttonVariants } from '@/components/ui/button';
import { cn } from '@/lib/utils';

interface ActionButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  children: React.ReactNode;
  /**
   * true: the text shows beside the icon; false: the icon only. 'wide': the
   * text shows on a bar of 896 px or more (the nearest @container), the icon
   * only below.
   */
  label?: boolean | 'wide';
  icon: ReactElement;
  variant?: VariantProps<typeof buttonVariants>['variant'];
}

const ActionButton = forwardRef<HTMLButtonElement, ActionButtonProps>(
  (
    { label, children, icon, className, disabled, onClick, variant, ...props },
    ref
  ) => {
    if (label === 'wide') {
      return (
        <Button
          ref={ref}
          size="icon-sm"
          variant={variant ?? 'outline'}
          disabled={disabled}
          onClick={onClick}
          // The label shows in a wide container. One least width for the
          // labels keeps the slot of the action as the action changes.
          className={cn(
            '@7xl:w-auto @7xl:min-w-[6.5rem] @7xl:gap-1.5 @7xl:px-2 @7xl:text-xs @7xl:font-semibold',
            className
          )}
          {...props}
        >
          <span className="h-4 w-4">{icon}</span>
          <span className="sr-only @7xl:not-sr-only">{children}</span>
        </Button>
      );
    }
    return label ? (
      <Button
        ref={ref}
        size="sm"
        variant={variant ?? 'default'}
        disabled={disabled}
        onClick={onClick}
        className={cn('font-semibold', className)}
        {...props}
      >
        <span className="h-4 w-4">{icon}</span>
        {children}
      </Button>
    ) : (
      <Button
        ref={ref}
        size="icon-sm"
        variant={variant ?? 'ghost'}
        disabled={disabled}
        onClick={onClick}
        className={className}
        {...props}
      >
        <span className="h-4 w-4">{icon}</span>
        <span className="sr-only">{children}</span>
      </Button>
    );
  }
);

ActionButton.displayName = 'ActionButton';

export type { ActionButtonProps };
export { ActionButton };
export default ActionButton;
