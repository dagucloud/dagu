import { Button } from '@/components/ui/button';
import ConfirmModal from '@/components/ui/confirm-dialog';
import { CopyButton } from '@/components/ui/copy-button';
import { useIsAdmin } from '@/contexts/AuthContext';
import { SelfUpgradeState } from '@/api/v1/schema';
import { useConfig } from '@/contexts/ConfigContext';
import { X } from 'lucide-react';
import * as React from 'react';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nTemplate } from '@/i18n/I18nTemplate';
import { I18nText } from '@/i18n/I18nText';
import { useIsMobile } from '@/hooks/useIsMobile';
import { useSelfUpgrade, type SelfUpgradeStatus } from '@/hooks/useSelfUpgrade';
import { cn } from '@/lib/utils';

const UPDATE_DISMISSED_KEY = 'update-banner-dismissed';
const RESTART_DISMISSED_KEY = 'restart-banner-dismissed';
const UPGRADE_GUIDE_URL = 'https://docs.dagu.sh/server-admin/upgrades';
const BANNER_CLASS =
  'bg-violet-50 dark:bg-[#1c1840] border-b border-violet-200 dark:border-[#3a3170] px-4 py-1.5 flex items-center justify-between gap-2 text-sm';
const KEEP_RUNS_DOCS_URL = `${UPGRADE_GUIDE_URL}#keep-runs-across-restarts`;

export function UpdateBanner() {
  const config = useConfig();
  const isAdmin = useIsAdmin();
  const phone = useIsMobile();
  const upgrade = useSelfUpgrade(config.version);

  // Only an administrator can upgrade or restart the server.
  if (!isAdmin) return null;

  // Restarting is the only step left once a new binary is installed, so it
  // replaces the update notice.
  if (config.restartRequired) {
    return (
      <DismissibleBanner
        key={RESTART_DISMISSED_KEY}
        storageKey={RESTART_DISMISSED_KEY}
        dismissedValue={config.version}
      >
        {config.restartCommand ? (
          <I18nTemplate
            text="A new Dagu binary is installed. Run {command} to restart and use it."
            values={{ command: <Command text={config.restartCommand} /> }}
          />
        ) : (
          <I18nText text="A new Dagu binary is installed. Restart Dagu to use it." />
        )}
        {config.restartStopsRuns && (
          <>
            {' '}
            {config.restartCanKeepRuns ? (
              <I18nTemplate
                text="The restart also ends runs in progress. {keepRunsLink}"
                values={{
                  keepRunsLink: (
                    <ExternalLink href={KEEP_RUNS_DOCS_URL}>
                      <I18nText text="Keep them running" />
                    </ExternalLink>
                  ),
                }}
              />
            ) : (
              <I18nText text="The restart also ends runs in progress." />
            )}
          </>
        )}
      </DismissibleBanner>
    );
  }

  if (!config.updateAvailable) return null;

  // An update started here replaces the notice until the page reloads.
  if (upgrade.status) {
    return (
      <BannerFrame>
        <UpgradeProgress
          status={upgrade.status}
          latest={config.latestVersion}
          slowRestart={upgrade.slowRestart}
          retryError={upgrade.requestError}
          onRetry={upgrade.start}
        />
      </BannerFrame>
    );
  }

  const values = {
    current: config.version,
    latest: config.latestVersion,
    guideLink: (
      <ExternalLink href={UPGRADE_GUIDE_URL} className="ml-2">
        <I18nText text="Upgrade guide" />
      </ExternalLink>
    ),
  };

  return (
    <DismissibleBanner
      key={UPDATE_DISMISSED_KEY}
      storageKey={UPDATE_DISMISSED_KEY}
      dismissedValue={minorSeries(config.latestVersion)}
    >
      {/* A phone has one line for it: the instruction is for a terminal. */}
      {phone ? (
        <I18nTemplate
          text="Update available: v{current} → {latest} {guideLink}"
          values={values}
        />
      ) : config.webUpgradeAvailable ? (
        <I18nTemplate
          text="Update available: v{current} → {latest} {guideLink} · {updateNow}"
          values={{
            ...values,
            updateNow: (
              <UpdateNowButton
                latest={config.latestVersion}
                restartStopsRuns={config.restartStopsRuns}
                error={upgrade.requestError}
                onConfirm={upgrade.start}
              />
            ),
          }}
        />
      ) : config.installMethod === 'container' ? (
        <I18nTemplate
          text="Update available: v{current} → {latest} {guideLink} · Update the container image to {latest}"
          values={values}
        />
      ) : config.upgradeCommand ? (
        <I18nTemplate
          text="Update available: v{current} → {latest} {guideLink} · Run {command} to update"
          values={{
            ...values,
            command: <Command text={config.upgradeCommand} />,
          }}
        />
      ) : (
        <I18nTemplate
          text="Update available: v{current} → {latest} {guideLink}"
          values={values}
        />
      )}
    </DismissibleBanner>
  );
}

// minorSeries returns the major and minor part of a version, such as v2.26
// for v2.26.3, so a dismissed notice stays hidden through patch releases.
function minorSeries(version: string): string {
  const match = /^v?(\d+)\.(\d+)\./.exec(version);
  return match ? `v${match[1]}.${match[2]}` : version;
}

// UpdateNowButton asks before it starts the update from the web UI.
function UpdateNowButton({
  latest,
  restartStopsRuns,
  error,
  onConfirm,
}: {
  latest: string;
  restartStopsRuns: boolean;
  error: string | null;
  onConfirm: () => void;
}) {
  const [confirming, setConfirming] = React.useState(false);
  return (
    <>
      <Button
        size="xs"
        variant="primary"
        className="-my-1"
        onClick={() => setConfirming(true)}
      >
        <I18nText text="Update now" />
      </Button>
      {error && (
        <span className="ml-2 text-destructive">
          <I18nTemplate
            text="The update did not start: {error}"
            values={{ error }}
          />
        </span>
      )}
      <I18nProps>
        <ConfirmModal
          title="Update Dagu"
          buttonText="Update"
          visible={confirming}
          dismissModal={() => setConfirming(false)}
          onSubmit={() => {
            setConfirming(false);
            onConfirm();
          }}
        >
          <p>
            <I18nTemplate
              text={
                restartStopsRuns
                  ? 'Dagu downloads {latest}, verifies its checksum, replaces its binary, and restarts. The restart also ends runs in progress.'
                  : 'Dagu downloads {latest}, verifies its checksum, replaces its binary, and restarts. Runs in progress keep running.'
              }
              values={{ latest }}
            />
          </p>
          {restartStopsRuns && (
            <p className="mt-2">
              <ExternalLink href={KEEP_RUNS_DOCS_URL}>
                <I18nText text="Keep them running" />
              </ExternalLink>
            </p>
          )}
        </ConfirmModal>
      </I18nProps>
    </>
  );
}

// UpgradeProgress tells how far an update started from the web UI got.
function UpgradeProgress({
  status,
  latest,
  slowRestart,
  retryError,
  onRetry,
}: {
  status: SelfUpgradeStatus;
  latest: string;
  slowRestart: boolean;
  retryError: string | null;
  onRetry: () => void;
}) {
  const version = status.targetVersion ?? latest;
  switch (status.state) {
    case SelfUpgradeState.failed:
      return (
        <>
          <I18nTemplate
            text="Update failed: {error}"
            values={{ error: status.error ?? '' }}
          />
          <Button
            size="xs"
            variant="outline"
            className="-my-1 ml-2"
            onClick={onRetry}
          >
            <I18nText text="Try again" />
          </Button>
          {retryError && (
            <span className="ml-2 text-destructive">
              <I18nTemplate
                text="The update did not start: {error}"
                values={{ error: retryError }}
              />
            </span>
          )}
        </>
      );
    case SelfUpgradeState.restarting:
      return slowRestart ? (
        <I18nText text="Dagu has not come back yet. Check the Dagu service on the server." />
      ) : (
        <I18nText text="Restarting Dagu…" />
      );
    case SelfUpgradeState.installing:
      return <I18nTemplate text="Installing {version}…" values={{ version }} />;
    default: {
      const { downloadedBytes: downloaded, totalBytes: total } = status;
      return downloaded !== undefined && total ? (
        <I18nTemplate
          text="Downloading {version}… {percent}%"
          values={{
            version,
            percent: Math.floor((downloaded * 100) / total),
          }}
        />
      ) : (
        <I18nTemplate text="Downloading {version}…" values={{ version }} />
      );
    }
  }
}

// BannerFrame lays out a banner line without a dismiss button.
function BannerFrame({ children }: { children: React.ReactNode }) {
  return (
    <div className={BANNER_CLASS}>
      <span className="min-w-0 text-violet-900 dark:text-violet-200">
        {children}
      </span>
    </div>
  );
}

// DismissibleBanner stays hidden while storageKey holds dismissedValue, so a
// new value brings it back.
function DismissibleBanner({
  storageKey,
  dismissedValue,
  children,
}: {
  storageKey: string;
  dismissedValue: string;
  children: React.ReactNode;
}) {
  const [dismissed, setDismissed] = React.useState(
    () => localStorage.getItem(storageKey) === dismissedValue
  );

  if (dismissed) return null;

  const handleDismiss = () => {
    localStorage.setItem(storageKey, dismissedValue);
    setDismissed(true);
  };

  return (
    <div className={BANNER_CLASS}>
      <span className="min-w-0 text-violet-900 dark:text-violet-200">
        {children}
      </span>
      <I18nProps>
        <button
          onClick={handleDismiss}
          className="shrink-0 p-0.5 hover:bg-violet-100 dark:hover:bg-[#2a2452] rounded"
          aria-label="Dismiss update notification"
        >
          <X className="h-4 w-4" />
        </button>
      </I18nProps>
    </div>
  );
}

function ExternalLink({
  href,
  className,
  children,
}: {
  href: string;
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <a
      href={href}
      target="_blank"
      rel="noopener noreferrer"
      className={cn('underline hover:no-underline', className)}
    >
      {children}
    </a>
  );
}

function Command({ text }: { text: string }) {
  const { ts } = useI18n();
  return (
    <span className="inline-flex items-center gap-0.5 align-middle">
      <code className="font-mono bg-violet-100 dark:bg-[#2a2452] px-1 rounded text-xs">
        {text}
      </code>
      <CopyButton
        text={text}
        label={ts('Copy command')}
        size="sm"
        className="-my-1 hover:bg-violet-100 dark:hover:bg-[#2a2452]"
      />
    </span>
  );
}
