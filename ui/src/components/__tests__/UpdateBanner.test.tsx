import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { UpdateBanner } from '@/components/UpdateBanner';
import { ConfigContext, type Config } from '@/contexts/ConfigContext';

const { isAdmin } = vi.hoisted(() => ({ isAdmin: vi.fn(() => true) }));
vi.mock('@/contexts/AuthContext', () => ({ useIsAdmin: isAdmin }));

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
});
