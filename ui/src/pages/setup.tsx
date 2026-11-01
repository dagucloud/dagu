import { useState, useEffect } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { useAuth } from '@/contexts/AuthContext';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { AuthLayout } from '@/components/AuthLayout';
import { useI18n } from '@/i18n/I18nProvider';
import { requestedPath } from '@/lib/requestedPath';
import { AlertCircle, UserPlus, Loader2 } from 'lucide-react';

function getErrorStatus(err: unknown): number | undefined {
  if (typeof err !== 'object' || err === null || !('status' in err)) {
    return undefined;
  }

  const status = (err as { status?: unknown }).status;
  return typeof status === 'number' ? status : undefined;
}

export default function SetupPage() {
  const { t } = useI18n();
  const { setupRequired, setup, completeSetup } = useAuth();
  const navigate = useNavigate();
  // Where the visitor was going, which the login page also returns to.
  const { state } = useLocation();

  const [setupDone, setSetupDone] = useState(false);

  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  // Redirect away if setup is already complete (e.g., user navigated here directly).
  // Skip if we just completed setup ourselves — we handle navigation manually.
  useEffect(() => {
    if (!setupRequired && !setupDone) {
      navigate('/login', { replace: true, state });
    }
  }, [setupRequired, setupDone, navigate, state]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);

    const trimmedUsername = username.trim();
    if (trimmedUsername.length < 3) {
      setError(t('setup.usernameMinLength'));
      return;
    }

    if (password.length < 8) {
      setError(t('auth.passwordMinLength'));
      return;
    }

    if (password !== confirmPassword) {
      setError(t('setup.passwordsDoNotMatch'));
      return;
    }

    setLoading(true);

    try {
      const result = await setup(trimmedUsername, password);
      setSetupDone(true);
      completeSetup(result);
      navigate(requestedPath(state), { replace: true });
    } catch (err) {
      if (getErrorStatus(err) === 403) {
        navigate('/login', { replace: true, state });
        return;
      }
      setError(err instanceof Error ? err.message : t('setup.failed'));
    } finally {
      setLoading(false);
    }
  };

  return (
    <AuthLayout subtitle={t('setup.createAdmin')}>
      {error && (
        <div className="flex items-center gap-2 p-3 text-sm text-destructive bg-destructive/10 rounded-md">
          <AlertCircle className="h-4 w-4 flex-shrink-0" />
          <span>{error}</span>
        </div>
      )}

      <form onSubmit={handleSubmit} className="space-y-4">
        <div className="space-y-1.5">
          <Label htmlFor="username" className="text-sm">
            {t('auth.username')}
          </Label>
          <Input
            id="username"
            type="text"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            required
            autoComplete="username"
            autoFocus
            className="h-9"
          />
        </div>

        <div className="space-y-1.5">
          <Label htmlFor="password" className="text-sm">
            {t('auth.password')}
          </Label>
          <Input
            id="password"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
            autoComplete="new-password"
            className="h-9"
          />
          <p className="text-xs text-muted-foreground">
            {t('setup.minimumCharacters')}
          </p>
        </div>

        <div className="space-y-1.5">
          <Label htmlFor="confirmPassword" className="text-sm">
            {t('setup.confirmPassword')}
          </Label>
          <Input
            id="confirmPassword"
            type="password"
            value={confirmPassword}
            onChange={(e) => setConfirmPassword(e.target.value)}
            required
            autoComplete="new-password"
            className="h-9"
          />
        </div>

        <Button
          type="submit"
          variant="primary"
          className="w-full h-9"
          disabled={loading}
        >
          {loading ? (
            <>
              <Loader2 className="h-4 w-4 animate-spin" />
              {t('setup.creatingAccount')}
            </>
          ) : (
            <>
              <UserPlus className="h-4 w-4" />
              {t('setup.createAccount')}
            </>
          )}
        </Button>
      </form>
    </AuthLayout>
  );
}
