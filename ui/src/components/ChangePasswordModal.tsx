import { useState } from 'react';
import { useConfig } from '@/contexts/ConfigContext';
import { useAuth } from '@/contexts/AuthContext';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { AlertCircle, CheckCircle, KeyRound, X } from 'lucide-react';
import { useI18n } from '@/i18n/I18nProvider';

type ChangePasswordModalProps = {
  open: boolean;
  onClose: () => void;
};

/**
 * Render a modal dialog that lets the current user change their password.
 *
 * Validates input (matching confirmation and minimum length), submits the change to the configured API, and displays error or success state. The modal resets its form when closed.
 *
 * @param open - Whether the modal is visible.
 * @param onClose - Callback invoked when the modal is closed.
 * @returns The Change Password modal React element.
 */
export function ChangePasswordModal({
  open,
  onClose,
}: ChangePasswordModalProps) {
  const { t } = useI18n();
  const config = useConfig();
  const { token } = useAuth();
  const [currentPassword, setCurrentPassword] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);
  const [isLoading, setIsLoading] = useState(false);

  const resetForm = () => {
    setCurrentPassword('');
    setNewPassword('');
    setConfirmPassword('');
    setError(null);
    setSuccess(false);
  };

  const handleClose = () => {
    resetForm();
    onClose();
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);

    if (newPassword !== confirmPassword) {
      setError(t('auth.passwordsDoNotMatch'));
      return;
    }

    if (newPassword.length < 8) {
      setError(t('auth.passwordMinLength'));
      return;
    }

    setIsLoading(true);

    try {
      const response = await fetch(`${config.apiURL}/auth/change-password`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify({
          currentPassword,
          newPassword,
        }),
      });

      if (!response.ok) {
        const data = await response.json().catch(() => ({}));
        // Check for nested error message in API response format
        const message =
          data.message || data.error?.message || t('auth.changePasswordFailed');
        throw new Error(message);
      }

      setSuccess(true);
      setTimeout(() => {
        handleClose();
      }, 1500);
    } catch (err) {
      setError(
        err instanceof Error ? err.message : t('auth.changePasswordFailed')
      );
    } finally {
      setIsLoading(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(isOpen) => !isOpen && handleClose()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t('auth.changePassword')}</DialogTitle>
        </DialogHeader>

        <form onSubmit={handleSubmit} className="space-y-4 mt-2">
          {error && (
            <div className="flex items-center gap-2 p-3 text-sm text-destructive bg-destructive/10 rounded-md">
              <AlertCircle className="h-4 w-4 flex-shrink-0" />
              <span>{error}</span>
            </div>
          )}

          {success && (
            <div className="flex items-center gap-2 p-3 text-sm text-success bg-success/10 rounded-md">
              <CheckCircle className="h-4 w-4 flex-shrink-0" />
              <span>{t('auth.passwordChanged')}</span>
            </div>
          )}

          <div className="space-y-1.5">
            <Label htmlFor="current-password" className="text-sm">
              {t('auth.currentPassword')}
            </Label>
            <Input
              id="current-password"
              type="password"
              value={currentPassword}
              onChange={(e) => setCurrentPassword(e.target.value)}
              required
              autoComplete="current-password"
              className="h-9"
            />
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="new-password" className="text-sm">
              {t('auth.newPassword')}
            </Label>
            <Input
              id="new-password"
              type="password"
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              required
              autoComplete="new-password"
              className="h-9"
            />
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="confirm-password" className="text-sm">
              {t('auth.confirmNewPassword')}
            </Label>
            <Input
              id="confirm-password"
              type="password"
              value={confirmPassword}
              onChange={(e) => setConfirmPassword(e.target.value)}
              required
              autoComplete="new-password"
              className="h-9"
            />
          </div>

          <div className="flex justify-end gap-2 pt-2">
            <Button type="button" variant="ghost" onClick={handleClose}>
              <X className="h-4 w-4" />
              {t('common.cancel')}
            </Button>
            <Button type="submit" disabled={isLoading || success}>
              <KeyRound className="h-4 w-4" />
              {isLoading
                ? t('auth.changingPassword')
                : t('auth.changePassword')}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
