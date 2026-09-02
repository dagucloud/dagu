import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { AlertTriangle, X } from 'lucide-react';
import { createContext, useContext, useState } from 'react';
import type { FC, ReactNode } from 'react';
import { I18nText } from '@/i18n/I18nText';

interface ErrorModalProps {
  title?: string;
  message: string;
  hint?: string;
  /** Machine-generated diagnostics (e.g. validation errors); rendered monospace with line breaks preserved. */
  details?: string[];
  isOpen: boolean;
  onClose: () => void;
}

export const ErrorModal: FC<ErrorModalProps> = ({
  title = 'Error',
  message,
  hint,
  details,
  isOpen,
  onClose,
}) => {
  const hasDetails = details != null && details.length > 0;
  return (
    <Dialog open={isOpen} onOpenChange={(open) => !open && onClose()}>
      <DialogContent
        className={hasDetails ? 'sm:max-w-[640px]' : 'sm:max-w-[450px]'}
      >
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 text-error">
            <AlertTriangle className="h-5 w-5" />
            {title}
          </DialogTitle>
        </DialogHeader>

        <div className="py-4 space-y-3">
          <p className="text-sm text-foreground">{message}</p>
          {hint && (
            <p className="text-xs text-muted-foreground bg-muted px-3 py-2 rounded-md">
              {hint}
            </p>
          )}
          {hasDetails && (
            <pre className="text-xs font-mono whitespace-pre-wrap break-words bg-muted px-3 py-2 rounded-md max-h-60 overflow-y-auto">
              {details.join('\n')}
            </pre>
          )}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            <X className="h-4 w-4 mr-1" />
            <I18nText text={"Close"} />
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
};

interface ErrorState {
  title?: string;
  message: string;
  hint?: string;
  details?: string[];
}

interface ErrorModalContextType {
  showError: (
    message: string,
    hint?: string,
    title?: string,
    details?: string[]
  ) => void;
}

export const ErrorModalContext = createContext<ErrorModalContextType>({
  showError: () => {},
});

interface ErrorModalProviderProps {
  children: ReactNode;
}

export const ErrorModalProvider: FC<ErrorModalProviderProps> = ({
  children,
}) => {
  const [error, setError] = useState<ErrorState | null>(null);

  const showError = (
    message: string,
    hint?: string,
    title?: string,
    details?: string[]
  ) => {
    setError({ message, hint, title, details });
  };

  const handleClose = () => {
    setError(null);
  };

  return (
    <ErrorModalContext.Provider value={{ showError }}>
      {children}
      {error && (
        <ErrorModal
          title={error.title}
          message={error.message}
          hint={error.hint}
          details={error.details}
          isOpen={true}
          onClose={handleClose}
        />
      )}
    </ErrorModalContext.Provider>
  );
};

export function useErrorModal(): ErrorModalContextType {
  const context = useContext(ErrorModalContext);
  if (context === undefined) {
    throw new Error('useErrorModal must be used within an ErrorModalProvider');
  }
  return context;
}
