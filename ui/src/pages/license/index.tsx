import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { AppBarContext } from '@/contexts/AppBarContext';
import {
  type LicenseStatus,
  useConfig,
  useUpdateConfig,
} from '@/contexts/ConfigContext';
import { useClient } from '@/hooks/api';
import { LICENSE_CONSOLE_URL } from '@/lib/constants';
import dayjs from '@/lib/dayjs';
import ConfirmModal from '@/components/ui/confirm-dialog';
import {
  AlertTriangle,
  CheckCircle2,
  Info,
  Shield,
  XCircle,
} from 'lucide-react';
import { useContext, useEffect, useState } from 'react';

export default function LicensePage() {
  const config = useConfig();
  const license = config?.license;
  const updateConfig = useUpdateConfig();
  const appBarContext = useContext(AppBarContext);
  const client = useClient();

  const [key, setKey] = useState('');
  const [activating, setActivating] = useState(false);
  const [deactivating, setDeactivating] = useState(false);
  const [showDeactivateConfirm, setShowDeactivateConfirm] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [successMessage, setSuccessMessage] = useState<string | null>(null);

  useEffect(() => {
    appBarContext.setTitle('License');
  }, [appBarContext]);

  const remoteNode = appBarContext.selectedRemoteNode || 'local';

  async function refreshLicenseStatus() {
    try {
      const { data } = await client.GET('/license/status', {
        params: { query: { remoteNode } },
      });
      if (data) {
        updateConfig({ license: data });
      }
    } catch {
      // Activation state is already applied; status revalidation is best-effort.
    }
  }

  async function handleActivate(e?: React.FormEvent) {
    if (e) e.preventDefault();
    if (!key.trim()) return;
    setActivating(true);
    setError(null);
    setSuccessMessage(null);
    try {
      const { data, error: apiError } = await client.POST('/license/activate', {
        params: { query: { remoteNode } },
        body: { key: key.trim() },
      });
      if (apiError) {
        throw new Error(apiError.message || 'Activation failed');
      }
      const nextLicense: LicenseStatus = {
        valid: true,
        plan: data?.plan || 'pro',
        features: data?.features || [],
        expiry: data?.expiry || '',
        gracePeriod: false,
        graceEndsAt: '',
        community: false,
        source: 'file',
        warningCode: '',
        error: '',
      };
      updateConfig({ license: nextLicense });
      void refreshLicenseStatus();
      setKey('');
      setSuccessMessage('License activated successfully.');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Activation failed');
    } finally {
      setActivating(false);
    }
  }

  async function handleDeactivate() {
    setShowDeactivateConfirm(false);
    setDeactivating(true);
    setError(null);
    setSuccessMessage(null);
    try {
      const { error: apiError } = await client.POST('/license/deactivate', {
        params: { query: { remoteNode } },
      });
      if (apiError) {
        throw new Error(apiError.message || 'Deactivation failed');
      }
      const nextLicense: LicenseStatus = {
        valid: false,
        plan: '',
        features: [],
        expiry: '',
        gracePeriod: false,
        graceEndsAt: '',
        community: true,
        source: '',
        warningCode: '',
        error: '',
      };
      updateConfig({ license: nextLicense });
      void refreshLicenseStatus();
      setSuccessMessage('License deactivated. Running in community mode.');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Deactivation failed');
    } finally {
      setDeactivating(false);
    }
  }

  if (!license) return null;

  return (
    <div className="flex flex-col gap-6 max-w-2xl">
      <div>
        <h1 className="text-lg font-semibold">License</h1>
        <p className="text-sm text-muted-foreground">
          View license status and activate a Dagu license or trial key.
        </p>
      </div>

      {/* Current status */}
      <div className="card-obsidian p-4 space-y-3">
        <div className="flex items-center gap-2 text-sm font-medium">
          <Shield className="h-4 w-4" />
          Current License
        </div>
        <div className="grid grid-cols-[120px_1fr] gap-y-2 text-sm">
          <span className="text-muted-foreground">Status</span>
          <span className="flex items-center gap-1.5">
            {license.error ? (
              <>
                <XCircle className="h-3.5 w-3.5 text-red-500" />
                License Error
              </>
            ) : license.gracePeriod ? (
              <>
                <XCircle className="h-3.5 w-3.5 text-amber-500" />
                Grace Period
              </>
            ) : license.valid ? (
              <>
                <CheckCircle2 className="h-3.5 w-3.5 text-green-500" />
                Active
              </>
            ) : license.community ? (
              'Community Edition'
            ) : (
              <>
                <XCircle className="h-3.5 w-3.5 text-red-500" />
                Inactive
              </>
            )}
          </span>

          <span className="text-muted-foreground">Plan</span>
          <span className="capitalize">{license.plan || 'community'}</span>

          <span className="text-muted-foreground">Features</span>
          <span>
            {license.features.length > 0 ? license.features.join(', ') : 'None'}
          </span>

          {license.expiry && (
            <>
              <span className="text-muted-foreground">Expires</span>
              <span>{dayjs(license.expiry).format('YYYY-MM-DD')}</span>
            </>
          )}
        </div>
        {license.error && (
          <div role="alert" className="text-sm text-red-600 dark:text-red-400">
            {license.error}
          </div>
        )}
      </div>

      {/* Deactivate license */}
      {!license.community && (
        <div className="card-obsidian p-4 space-y-3">
          <div className="text-sm font-medium">Deactivate License</div>
          {license.source === 'env' ? (
            <div className="flex items-start gap-2 text-sm text-muted-foreground">
              <Info className="h-4 w-4 mt-0.5 flex-shrink-0" />
              <span>
                This license is configured via an environment variable (
                <code className="text-xs">DAGU_LICENSE</code> or{' '}
                <code className="text-xs">DAGU_LICENSE_KEY</code>). To
                deactivate, remove the environment variable and restart Dagu.
              </span>
            </div>
          ) : (
            <>
              <p className="text-sm text-muted-foreground">
                Remove the license from this machine and return to community
                mode.
              </p>
              <Button
                variant="destructive"
                size="sm"
                className="h-8"
                disabled={deactivating}
                onClick={() => setShowDeactivateConfirm(true)}
              >
                <AlertTriangle className="h-3.5 w-3.5" />
                {deactivating ? 'Deactivating...' : 'Deactivate License'}
              </Button>
            </>
          )}
        </div>
      )}

      {/* Activation form */}
      <div className="card-obsidian p-4 space-y-3">
        <div className="text-sm font-medium">Activate License Key</div>
        <form onSubmit={handleActivate} className="flex gap-2">
          <Input
            value={key}
            onChange={(e) => setKey(e.target.value)}
            placeholder="DAGU-XXXX-XXXX-XXXX-XXXX"
            className="font-mono text-sm h-8"
            aria-label="License key"
          />
          <Button
            type="submit"
            size="sm"
            className="h-8 flex-shrink-0"
            disabled={activating || !key.trim()}
          >
            {activating ? 'Activating...' : 'Activate'}
          </Button>
        </form>
        {error && (
          <div role="alert" className="text-sm text-destructive">
            {error}
          </div>
        )}
        {successMessage && (
          <div
            role="status"
            className="text-sm text-green-600 dark:text-green-400"
          >
            {successMessage}
          </div>
        )}
        <p className="text-xs text-muted-foreground">
          Enter your license or trial key to activate Dagu features. You can
          obtain a key from{' '}
          <a
            href={LICENSE_CONSOLE_URL}
            target="_blank"
            rel="noopener noreferrer"
            className="underline hover:no-underline"
          >
            console.dagu.sh
          </a>
          .
        </p>
      </div>

      <ConfirmModal
        title="Deactivate License"
        buttonText="Deactivate"
        visible={showDeactivateConfirm}
        dismissModal={() => setShowDeactivateConfirm(false)}
        onSubmit={handleDeactivate}
      >
        <p className="text-sm">
          This will deactivate the license on this machine and return to
          community mode. Licensed features (audit, RBAC, SSO) will be disabled
          immediately.
        </p>
      </ConfirmModal>
    </div>
  );
}
