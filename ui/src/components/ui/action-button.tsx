import type { VariantProps } from 'class-variance-authority';
import React, { ReactElement, forwardRef } from 'react';

import { Button, buttonVariants } from '@/components/ui/button';
import { cn } from '@/lib/utils';

interface ActionButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  children: React.ReactNode;
  label?: boolean;
  icon: ReactElement;
  variant?: VariantProps<typeof buttonVariants>['variant'];
}

const ActionButton = forwardRef<HTMLButtonElement, ActionButtonProps>(
  (
    { label, children, icon, className, disabled, onClick, variant, ...props },
    ref
  ) => {
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
