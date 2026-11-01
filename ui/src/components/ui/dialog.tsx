'use client';

import * as DialogPrimitive from '@radix-ui/react-dialog';
import { X } from 'lucide-react';
import * as React from 'react';

import { cn } from '@/lib/utils';
import { I18nText } from '@/i18n/I18nText';

const Dialog = DialogPrimitive.Root;

const DialogTrigger = DialogPrimitive.Trigger;

const DialogPortal = DialogPrimitive.Portal;

const DialogClose = DialogPrimitive.Close;

/**
 * The layer of a dialog. 'default' is z-50. 'raised' is z-[80], the Z.dialog
 * layer of the workspace, above the node view (z-[60]) and the menus.
 */
type DialogLayer = 'default' | 'raised';

const RAISED_LAYER = 'z-[80]';

const DialogLayerContext = React.createContext<DialogLayer>('default');

/** Every dialog below this provider takes the layer, unless it sets its own. */
const DialogLayerProvider = DialogLayerContext.Provider;

/**
 * The z-index class for a popup (select, menu, tooltip) that renders in a
 * portal. Inside a raised dialog or workspace it must sit above the dialog
 * overlay; elsewhere it keeps its own layer.
 */
function useRaisedLayerClass(): string | undefined {
  return useIsRaisedLayer() ? RAISED_LAYER : undefined;
}

/** True below a raised layer provider, for drawers that sit under dialogs. */
function useIsRaisedLayer(): boolean {
  return React.useContext(DialogLayerContext) === 'raised';
}

const DialogOverlay = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Overlay>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Overlay>
>(({ className, ...props }, ref) => (
  <DialogPrimitive.Overlay
    ref={ref}
    className={cn(
      'fixed inset-0 z-50 bg-black/40 backdrop-blur-[2px] dark:bg-black/60 data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0',
      className
    )}
    {...props}
  />
));
DialogOverlay.displayName = DialogPrimitive.Overlay.displayName;

const DialogContent = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Content>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Content> & {
    hideCloseButton?: boolean;
    fullscreen?: boolean;
    /** Defaults to the layer of the nearest DialogLayerProvider. */
    layer?: DialogLayer;
  }
>(
  (
    {
      className,
      children,
      hideCloseButton,
      fullscreen = false,
      layer,
      ...props
    },
    ref
  ) => {
    const inherited = React.useContext(DialogLayerContext);
    const raised = (layer ?? inherited) === 'raised';
    return (
      <DialogPortal>
        <DialogOverlay className={raised ? RAISED_LAYER : undefined} />
        <DialogPrimitive.Content
          ref={ref}
          className={cn(
            fullscreen
              ? 'fixed inset-0 z-50 grid h-[100dvh] max-h-[100dvh] w-screen max-w-none gap-0 overflow-hidden border border-border bg-background p-0 shadow-lg duration-150 data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0'
              : 'fixed left-[50%] top-[50%] z-50 grid w-full max-w-lg translate-x-[-50%] translate-y-[-50%] gap-4 border border-border bg-card p-6 shadow-lg duration-150 data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95 sm:rounded-xl',
            raised && RAISED_LAYER,
            className
          )}
          {...props}
        >
          <DialogLayerContext.Provider value={raised ? 'raised' : 'default'}>
            {children}
          </DialogLayerContext.Provider>
          {!hideCloseButton && (
            <DialogPrimitive.Close className="absolute right-3 top-3 rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:pointer-events-none">
              <X className="h-4 w-4" />
              <span className="sr-only">
                <I18nText text={'Close'} />
              </span>
            </DialogPrimitive.Close>
          )}
        </DialogPrimitive.Content>
      </DialogPortal>
    );
  }
);
DialogContent.displayName = DialogPrimitive.Content.displayName;

const DialogHeader = ({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) => (
  <div
    className={cn(
      'flex flex-col space-y-1 text-center sm:text-left',
      className
    )}
    {...props}
  />
);
DialogHeader.displayName = 'DialogHeader';

const DialogFooter = ({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) => (
  <div
    className={cn(
      'flex flex-col-reverse sm:flex-row sm:justify-end sm:space-x-2 pt-2',
      className
    )}
    {...props}
  />
);
DialogFooter.displayName = 'DialogFooter';

const DialogTitle = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Title>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Title>
>(({ className, ...props }, ref) => (
  <DialogPrimitive.Title
    ref={ref}
    className={cn('text-base font-semibold leading-tight', className)}
    {...props}
  />
));
DialogTitle.displayName = DialogPrimitive.Title.displayName;

const DialogDescription = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Description>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Description>
>(({ className, ...props }, ref) => (
  <DialogPrimitive.Description
    ref={ref}
    className={cn('text-sm text-muted-foreground', className)}
    {...props}
  />
));
DialogDescription.displayName = DialogPrimitive.Description.displayName;

export type { DialogLayer };
export {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogLayerProvider,
  DialogOverlay,
  DialogPortal,
  DialogTitle,
  DialogTrigger,
  useIsRaisedLayer,
  useRaisedLayerClass,
};
