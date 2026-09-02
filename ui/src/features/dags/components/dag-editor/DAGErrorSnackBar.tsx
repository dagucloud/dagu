/**
 * DAGErrorSnackBar component displays a snackbar with error messages.
 *
 * @module features/dags/components/dag-editor
 */
import { Alert } from '@/components/ui/alert';
import {
  Toast,
  ToastClose,
  ToastDescription,
  ToastProvider,
  ToastTitle,
} from '@/components/ui/toast';
import { I18nText } from '@/i18n/I18nText';

/**
 * Props for the DAGErrorSnackBar component
 */
type DAGErrorSnackBarProps = {
  /** Whether the snackbar is open */
  open: boolean;
  /** Function to set the open state */
  setOpen: (open: boolean) => void;
  /** List of error messages */
  errors: string[];
};

/**
 * DAGErrorSnackBar displays a snackbar with error messages
 * that automatically hides after a timeout
 */
const DAGErrorSnackBar = ({ open, setOpen, errors }: DAGErrorSnackBarProps) => {
  /**
   * Handle closing the snackbar
   */
  const handleClose = () => {
    setOpen(false);
  };

  // If not open, don't render anything
  if (!open) return null;

  return (
    <ToastProvider>
      <div className="fixed top-4 left-1/2 transform -translate-x-1/2 z-50 w-[20vw] max-w-md">
        <Toast variant="destructive" className="bg-card border-error">
          <div className="flex flex-col items-center w-full">
            <ToastTitle className="text-error text-xl font-bold">
              <I18nText text={"Error Detected"} />
            </ToastTitle>

            <ToastDescription className="text-error text-lg mt-1">
              <I18nText text={"Please check the following errors:"} />
            </ToastDescription>

            <div className="w-full mt-2">
              {errors.map((error, index) => (
                <Alert
                  key={index}
                  variant="destructive"
                  className="mb-2 bg-error-muted text-error text-sm py-2"
                >
                  {error}
                </Alert>
              ))}
            </div>
          </div>

          <ToastClose onClick={handleClose} />
        </Toast>
      </div>
    </ToastProvider>
  );
};

export default DAGErrorSnackBar;
