import { useState, useContext } from 'react';
import { useConfig } from '@/contexts/ConfigContext';
import { AppBarContext } from '@/contexts/AppBarContext';
import { components } from '@/api/v1/schema';
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
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';

type User = components['schemas']['User'];

type ResetPasswordModalProps = {
  open: boolean;
  user?: User;
  onClose: () => void;
};

/**
 * Renders a modal dialog allowing an administrator to set a new password for a user.
 *
 * Validates that the new and confirm passwords match and are at least 8 characters, sends
 * a PUT request to update the user's password using the auth token from localStorage, and
 * shows inline error or success feedback. The dialog resets its form state when closed.
 *
 * @param open - Whether the dialog is visible
 * @param user - The target user whose password will be reset
 * @param onClose - Callback invoked when the dialog is closed
 * @returns The Reset Password modal component
 */
export function ResetPasswordModal({
  open,
  user,
  onClose,
}: ResetPasswordModalProps) {
  const config = useConfig();
  const appBarContext = useContext(AppBarContext);
  const [newPassword, setNewPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);
  const [isLoading, setIsLoading] = useState(false);

  const resetForm = () => {
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

    if (!user) return;

    if (newPassword !== confirmPassword) {
      setError('Passwords do not match');
      return;
    }

    if (newPassword.length < 8) {
      setError('Password must be at least 8 characters');
      return;
    }

    setIsLoading(true);

    try {
      const token = localStorage.getItem('dagu_auth_token');
      if (!token) {
        throw new Error('Not authenticated');
      }
      const remoteNode = encodeURIComponent(
        appBarContext.selectedRemoteNode || 'local'
      );
      const response = await fetch(
        `${config.apiURL}/users/${user.id}/reset-password?remoteNode=${remoteNode}`,
        {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
            Authorization: `Bearer ${token}`,
          },
          body: JSON.stringify({ newPassword }),
        }
      );

      if (!response.ok) {
        const data = await response.json().catch(() => ({}));
        throw new Error(data.message || 'Failed to reset password');
      }

      setSuccess(true);
      setTimeout(() => {
        handleClose();
      }, 1500);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to reset password');
    } finally {
      setIsLoading(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(isOpen) => !isOpen && handleClose()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>
            <I18nText
              text="Reset Password for {username}"
              values={{ username: user?.username ?? '' }}
            />
          </DialogTitle>
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
              <span>
                <I18nText text={'Password reset successfully!'} />
              </span>
            </div>
          )}

          <div className="space-y-1.5">
            <Label htmlFor="new-password" className="text-sm">
              <I18nText text={'New Password'} />
            </Label>
            <I18nProps>
              <Input
                id="new-password"
                type="password"
                value={newPassword}
                onChange={(e) => setNewPassword(e.target.value)}
                required
                autoComplete="new-password"
                className="h-9"
                placeholder="Minimum 8 characters"
              />
            </I18nProps>
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="confirm-password" className="text-sm">
              <I18nText text={'Confirm Password'} />
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
              <I18nText text={'Cancel'} />
            </Button>
            <Button type="submit" disabled={isLoading || success}>
              <KeyRound className="h-4 w-4" />
              {isLoading ? (
                <I18nText text={'Resetting...'} />
              ) : (
                <I18nText text={'Reset Password'} />
              )}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
