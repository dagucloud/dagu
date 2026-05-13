import { useState, useEffect } from 'react';
import { useNavigate, useLocation, useSearchParams } from 'react-router-dom';
import { useAuth } from '@/contexts/AuthContext';
import { useConfig } from '@/contexts/ConfigContext';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { setAuthSession } from '@/lib/authSession';
import { AlertCircle, LogIn, KeyRound, CheckCircle } from 'lucide-react';

export default function LoginPage() {
  const config = useConfig();
  const { login, isAuthenticated, isLoading: authLoading, setupRequired } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const [searchParams] = useSearchParams();

  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [welcomeMessage, setWelcomeMessage] = useState<string | null>(null);

  const from = (location.state as { from?: Location })?.from?.pathname || '/';

  // Handle OIDC callback token and messages from URL params
  useEffect(() => {
    const tokenParam = searchParams.get('token');
    const errorParam = searchParams.get('error');
    const welcomeParam = searchParams.get('welcome');

    // Handle OIDC callback token - store in localStorage and navigate to home
    if (tokenParam) {
      setAuthSession(tokenParam, null, 'oidc');
      // Navigate to home immediately - AuthProvider will validate token on next page load
      navigate(from, { replace: true });
      return;
    }

    if (errorParam) {
      setError(errorParam);
    }
    if (welcomeParam === 'true') {
      setWelcomeMessage('Welcome! Your account has been created.');
    }
  }, [searchParams, navigate, from]);

  // Redirect to setup page if initial admin account hasn't been created.
  // Wait for auth state to settle (isLoading=false) to avoid acting on
  // stale static config before the dynamic API check completes.
  useEffect(() => {
    if (!authLoading && setupRequired) {
      navigate('/setup', { replace: true });
    }
  }, [authLoading, setupRequired, navigate]);

  // Redirect if already authenticated - use useEffect to avoid render-phase side effects
  useEffect(() => {
    if (isAuthenticated) {
      navigate(from, { replace: true });
    }
  }, [isAuthenticated, navigate, from]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    setIsLoading(true);

    try {
      await login(username, password);
      navigate(from, { replace: true });
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Login failed');
    } finally {
      setIsLoading(false);
    }
  };

  const handleOIDCLogin = () => {
    // Redirect to OIDC login endpoint
    window.location.href = `${config.basePath}/oidc-login`;
  };

  return (
    <div className="min-h-screen flex items-center justify-center bg-muted/50">
      <div className="w-full max-w-sm p-6 space-y-6">
        <div className="text-center space-y-2">
          <h1 className="text-2xl font-bold">{config.title || 'Dagu'}</h1>
          <p className="text-sm text-muted-foreground">Sign in to your account</p>
        </div>

        <div className="space-y-4">
          {welcomeMessage && (
            <div className="flex items-center gap-2 p-3 text-sm text-green-700 dark:text-green-400 bg-green-100 dark:bg-green-900/30 rounded-md">
              <CheckCircle className="h-4 w-4 flex-shrink-0" />
              <span>{welcomeMessage}</span>
            </div>
          )}

          {error && (
            <div className="flex items-center gap-2 p-3 text-sm text-destructive bg-destructive/10 rounded-md">
              <AlertCircle className="h-4 w-4 flex-shrink-0" />
              <span>{error}</span>
            </div>
          )}

          <form onSubmit={handleSubmit} className="space-y-4">
            <div className="space-y-1.5">
              <Label htmlFor="username" className="text-sm">
                Username
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
                Password
              </Label>
              <Input
                id="password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
                autoComplete="current-password"
                className="h-9"
              />
            </div>

            <Button type="submit" className="w-full h-9" disabled={isLoading}>
              <LogIn className="h-4 w-4" />
              {isLoading ? 'Signing in...' : 'Sign In'}
            </Button>
          </form>

          {config.oidcEnabled && (
            <>
              <div className="relative">
                <div className="absolute inset-0 flex items-center">
                  <span className="w-full border-t" />
                </div>
                <div className="relative flex justify-center text-xs uppercase">
                  <span className="bg-background px-2 text-muted-foreground">or</span>
                </div>
              </div>

              <Button
                type="button"
                variant="outline"
                className="w-full h-9"
                onClick={handleOIDCLogin}
              >
                <KeyRound className="h-4 w-4" />
                {config.oidcButtonLabel || 'Login with SSO'}
              </Button>
            </>
          )}
        </div>
      </div>
    </div>
  );
}
