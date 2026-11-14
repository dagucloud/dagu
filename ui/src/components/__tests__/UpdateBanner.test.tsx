import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { UpdateBanner } from '@/components/UpdateBanner';
import { ConfigContext, type Config } from '@/contexts/ConfigContext';

const { isAdmin, client } = vi.hoisted(() => ({
  isAdmin: vi.fn(() => true),
  client: { GET: vi.fn(), POST: vi.fn() },
}));
vi.mock('@/contexts/AuthContext', () => ({ useIsAdmin: isAdmin }));
vi.mock('@/hooks/api', () => ({ useClient: () => client }));

const updateConfig = {
  version: '2.0.0',
  updateAvailable: true,
  latestVersion: 'v2.1.0',
  upgradeCommand: 'dagu upgrade',
  installMethod: 'binary',
  restartRequired: false,
  restartCommand: '',
  restartStopsRuns: false,
  restartCanKeepRuns: false,
  webUpgradeAvailable: false,
} satisfies Partial<Config>;

// The banner reads only the update fields, so the rest of Config is omitted.
function renderBanner(overrides: Partial<Config> = {}) {
  const config = { ...updateConfig, ...overrides } as Config;
  return render(
    <ConfigContext.Provider value={config}>
      <UpdateBanner />
    </ConfigContext.Provider>
  );
}

describe('UpdateBanner', () => {
  beforeEach(() => {
    localStorage.clear();
    isAdmin.mockReturnValue(true);
    client.GET.mockReset();
    client.POST.mockReset();
  });

  // Only an administrator can upgrade or restart the server.
  it('renders nothing for a user who is not an administrator', () => {
    isAdmin.mockReturnValue(false);
    const { container } = renderBanner({ restartRequired: true });

    expect(container).toBeEmptyDOMElement();
  });

  it('links to the upgrade guide', () => {
    renderBanner();

    expect(screen.getByRole('link', { name: 'Upgrade guide' })).toHaveAttribute(
      'href',
      'https://docs.dagu.sh/server-admin/upgrades'
    );
  });

  it('offers to copy the update command', () => {
    renderBanner();

    expect(
      screen.getByRole('button', { name: 'Copy command' })
    ).toBeInTheDocument();
  });

  it('tells a container install to update its image', () => {
    renderBanner({ installMethod: 'container', upgradeCommand: '' });

    expect(
      screen.getByText(/Update the container image to v2\.1\.0/)
    ).toBeInTheDocument();
  });

  it('renders nothing when Dagu is up to date', () => {
    const { container } = renderBanner({ updateAvailable: false });

    expect(container).toBeEmptyDOMElement();
  });

  it('shows the update command for this installation', () => {
    renderBanner({ upgradeCommand: 'brew upgrade dagu' });

    expect(screen.getByText(/v2\.0\.0 → v2\.1\.0/)).toBeInTheDocument();
    expect(screen.getByText('brew upgrade dagu')).toBeInTheDocument();
    expect(screen.queryByText('dagu upgrade')).not.toBeInTheDocument();
  });

  // A phone has one line for the banner: the command is for a terminal.
  it('leaves the command out on a phone', () => {
    const width = window.innerWidth;
    window.innerWidth = 390;
    try {
      renderBanner({ upgradeCommand: 'brew upgrade dagu' });
      expect(screen.getByText(/v2\.0\.0 → v2\.1\.0/)).toBeInTheDocument();
      expect(screen.queryByText('brew upgrade dagu')).not.toBeInTheDocument();
    } finally {
      window.innerWidth = width;
    }
  });

  it('omits the run instruction when no single command updates Dagu', () => {
    renderBanner({ upgradeCommand: '' });

    expect(screen.getByText(/v2\.0\.0 → v2\.1\.0/)).toBeInTheDocument();
    expect(screen.queryByText(/to update/)).not.toBeInTheDocument();
  });

  it('asks for a restart once a new binary is installed', () => {
    renderBanner({
      restartRequired: true,
      restartCommand: 'sudo systemctl restart dagu',
    });

    expect(
      screen.getByText(/A new Dagu binary is installed/)
    ).toBeInTheDocument();
    expect(screen.getByText('sudo systemctl restart dagu')).toBeInTheDocument();
    expect(screen.queryByText('dagu upgrade')).not.toBeInTheDocument();
    expect(screen.queryByText(/ends runs in progress/)).not.toBeInTheDocument();
  });

  it('warns that the restart ends runs and links how to keep them', () => {
    renderBanner({
      restartRequired: true,
      restartCommand: 'sudo systemctl restart dagu',
      restartStopsRuns: true,
      restartCanKeepRuns: true,
    });

    expect(
      screen.getByText(/The restart also ends runs in progress/)
    ).toBeInTheDocument();
    expect(
      screen.getByRole('link', { name: 'Keep them running' })
    ).toHaveAttribute(
      'href',
      'https://docs.dagu.sh/server-admin/upgrades#keep-runs-across-restarts'
    );
  });

  it('warns without a link when the service cannot keep runs', () => {
    renderBanner({
      restartRequired: true,
      restartCommand: 'Restart-Service Dagu',
      restartStopsRuns: true,
    });

    expect(
      screen.getByText(/The restart also ends runs in progress/)
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('link', { name: 'Keep them running' })
    ).not.toBeInTheDocument();
  });

  it('asks for a restart without a command when no service is known', () => {
    renderBanner({ updateAvailable: false, restartRequired: true });

    expect(
      screen.getByText(
        'A new Dagu binary is installed. Restart Dagu to use it.'
      )
    ).toBeInTheDocument();
  });

  // Patch releases come out several times a week; a minor release is news.
  it('stays dismissed through patch releases until the next minor release', async () => {
    const user = userEvent.setup();
    const { unmount } = renderBanner();

    await user.click(
      screen.getByRole('button', { name: 'Dismiss update notification' })
    );
    expect(screen.queryByText(/v2\.1\.0/)).not.toBeInTheDocument();

    unmount();
    const { unmount: unmountPatch } = renderBanner({ latestVersion: 'v2.1.3' });
    expect(screen.queryByText(/v2\.1\.3/)).not.toBeInTheDocument();

    unmountPatch();
    renderBanner({ latestVersion: 'v2.2.0' });
    expect(screen.getByText(/v2\.0\.0 → v2\.2\.0/)).toBeInTheDocument();
  });

  it('shows the restart notice again after dismissing the update notice', async () => {
    const user = userEvent.setup();
    const { unmount } = renderBanner();

    await user.click(
      screen.getByRole('button', { name: 'Dismiss update notification' })
    );
    unmount();
    renderBanner({ restartRequired: true });

    expect(
      screen.getByText(/A new Dagu binary is installed/)
    ).toBeInTheDocument();
  });

  describe('update from the web UI', () => {
    // startUpdate confirms the update the banner offers.
    async function startUpdate(user: ReturnType<typeof userEvent.setup>) {
      await user.click(screen.getByRole('button', { name: 'Update now' }));
      await user.click(screen.getByRole('button', { name: 'Update' }));
    }

    it('offers to update in place of the command', () => {
      renderBanner({ webUpgradeAvailable: true });

      expect(
        screen.getByRole('button', { name: 'Update now' })
      ).toBeInTheDocument();
      expect(screen.queryByText('dagu upgrade')).not.toBeInTheDocument();
    });

    it('warns before an update whose restart ends runs', async () => {
      const user = userEvent.setup();
      renderBanner({ webUpgradeAvailable: true, restartStopsRuns: true });

      await user.click(screen.getByRole('button', { name: 'Update now' }));

      expect(
        screen.getByText(/The restart also ends runs in progress/)
      ).toBeInTheDocument();
    });

    it('shows the progress of the update it started', async () => {
      const user = userEvent.setup();
      client.POST.mockResolvedValue({
        data: {
          available: true,
          state: 'downloading',
          targetVersion: 'v2.1.0',
          downloadedBytes: 25,
          totalBytes: 100,
        },
      });
      renderBanner({ webUpgradeAvailable: true });

      await startUpdate(user);

      expect(client.POST).toHaveBeenCalledWith('/system/upgrade', {
        params: { query: { remoteNode: 'local' } },
      });
      expect(
        await screen.findByText('Downloading v2.1.0… 25%')
      ).toBeInTheDocument();
      expect(
        screen.queryByRole('button', { name: 'Dismiss update notification' })
      ).not.toBeInTheDocument();
    });

    it('shows the restart', async () => {
      const user = userEvent.setup();
      client.POST.mockResolvedValue({
        data: { available: true, state: 'restarting' },
      });
      client.GET.mockReturnValue(new Promise(() => {}));
      renderBanner({ webUpgradeAvailable: true });

      await startUpdate(user);

      expect(await screen.findByText('Restarting Dagu…')).toBeInTheDocument();
    });

    it('offers to try a failed update again', async () => {
      const user = userEvent.setup();
      client.POST.mockResolvedValue({
        data: {
          available: true,
          state: 'failed',
          error: 'checksum mismatch',
        },
      });
      renderBanner({ webUpgradeAvailable: true });

      await startUpdate(user);
      expect(
        await screen.findByText('Update failed: checksum mismatch')
      ).toBeInTheDocument();

      client.POST.mockResolvedValue({
        error: { message: 'an upgrade is already in progress' },
      });
      await user.click(screen.getByRole('button', { name: 'Try again' }));
      expect(client.POST).toHaveBeenCalledTimes(2);
      expect(
        await screen.findByText(
          'The update did not start: an upgrade is already in progress'
        )
      ).toBeInTheDocument();
    });

    it('says why the update did not start', async () => {
      const user = userEvent.setup();
      client.POST.mockResolvedValue({
        error: { message: 'an upgrade is already in progress' },
      });
      renderBanner({ webUpgradeAvailable: true });

      await startUpdate(user);

      expect(
        await screen.findByText(
          'The update did not start: an upgrade is already in progress'
        )
      ).toBeInTheDocument();
    });
  });
});
