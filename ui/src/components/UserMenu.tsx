import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useAuth } from '@/contexts/AuthContext';
import { useConfig } from '@/contexts/ConfigContext';
import { UserAuthProvider } from '@/api/v1/schema';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { User, LogOut, Key } from 'lucide-react';
import { ChangePasswordModal } from './ChangePasswordModal';
import { useI18n } from '@/i18n/I18nProvider';

type UserMenuProps = {
  isCollapsed?: boolean;
};

/**
 * Renders a user dropdown menu with profile info, a change-password action, and sign-out.
 *
 * The menu is rendered only when built-in authentication is enabled and a user is authenticated.
 *
 * @returns The user menu JSX element when shown, or `null` when authentication is not available.
 */
export function UserMenu({ isCollapsed = false }: UserMenuProps) {
  const { t } = useI18n();
  const { user, logout, isAuthenticated } = useAuth();
  const config = useConfig();
  const navigate = useNavigate();
  const [showChangePassword, setShowChangePassword] = useState(false);

  // Don't show if auth is not builtin or user is not authenticated
  if (config.authMode !== 'builtin' || !isAuthenticated || !user) {
    return null;
  }

  const handleLogout = () => {
    logout();
    navigate('/login');
  };

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <button
            className="flex items-center gap-2 h-7 px-2 rounded hover:bg-sidebar-foreground/5 text-sidebar-foreground cursor-pointer w-full"
            style={{ transition: 'background-color 150ms ease' }}
            title={isCollapsed ? user.username : undefined}
          >
            <User className="h-4 w-4 shrink-0" />
            <span
              className="text-xs font-medium overflow-hidden whitespace-nowrap"
              style={{
                transition:
                  'opacity 200ms cubic-bezier(0.4, 0, 0.2, 1), transform 200ms cubic-bezier(0.4, 0, 0.2, 1), max-width 280ms cubic-bezier(0.4, 0, 0.2, 1)',
                opacity: isCollapsed ? 0 : 1,
                maxWidth: isCollapsed ? '0px' : '120px',
                transform: isCollapsed ? 'translateX(-8px)' : 'translateX(0)',
              }}
            >
              {user.username}
            </span>
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent
          align={isCollapsed ? 'center' : 'end'}
          side="top"
          className="w-56"
        >
          <DropdownMenuLabel className="font-normal">
            <div className="flex flex-col space-y-1">
              <p className="text-sm font-medium">{user.username}</p>
              <span className="text-xs px-1.5 py-0.5 rounded bg-muted text-muted-foreground capitalize w-fit">
                {user.role}
              </span>
            </div>
          </DropdownMenuLabel>
          <DropdownMenuSeparator />
          {(!user.authProvider ||
            user.authProvider === UserAuthProvider.builtin) && (
            <>
              <DropdownMenuItem onClick={() => setShowChangePassword(true)}>
                <Key className="h-4 w-4 mr-2" />
                {t('auth.changePassword')}
              </DropdownMenuItem>
              <DropdownMenuSeparator />
            </>
          )}
          <DropdownMenuItem
            onClick={handleLogout}
            className="text-error focus:text-error"
          >
            <LogOut className="h-4 w-4 mr-2" />
            {t('auth.signOut')}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <ChangePasswordModal
        open={showChangePassword}
        onClose={() => setShowChangePassword(false)}
      />
    </>
  );
}
