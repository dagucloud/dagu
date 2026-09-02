import { components, UserAuthProvider } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { AppBarContext } from '@/contexts/AppBarContext';
import { TOKEN_KEY, useAuth, useIsAdmin } from '@/contexts/AuthContext';
import { useConfig } from '@/contexts/ConfigContext';
import { useHasFeature } from '@/hooks/useLicense';
import dayjs from '@/lib/dayjs';
import ConfirmModal from '@/components/ui/confirm-dialog';
import {
  Ban,
  Info,
  Key,
  MoreHorizontal,
  Pencil,
  Trash2,
  UserCheck,
  UserPlus,
} from 'lucide-react';
import { useCallback, useContext, useEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import { ResetPasswordModal } from './ResetPasswordModal';
import { UserFormModal } from './UserFormModal';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nTemplate } from '@/i18n/I18nTemplate';

type User = components['schemas']['User'];
type UsersListResponse = components['schemas']['UsersListResponse'];

function AuthProviderBadge({
  user,
  managedRoleProviders,
  managedWorkspaceAccessProviders,
}: {
  user: User;
  managedRoleProviders: UserAuthProvider[];
  managedWorkspaceAccessProviders: UserAuthProvider[];
}) {
  const { ts } = useI18n();
  const provider = user.authProvider ?? UserAuthProvider.builtin;
  if (provider === UserAuthProvider.builtin) {
    return 'Local';
  }
  const providerLabel = provider === UserAuthProvider.oidc ? 'SSO' : 'Proxy';
  const roleManaged = managedRoleProviders.includes(provider);
  const workspaceAccessManaged =
    managedWorkspaceAccessProviders.includes(provider);
  if (!roleManaged && !workspaceAccessManaged) {
    return providerLabel;
  }
  if (roleManaged && workspaceAccessManaged) {
    return (
      <Badge variant="info">
        {ts('Managed by {provider}', { provider: providerLabel })}
      </Badge>
    );
  }
  const scope = ts(roleManaged ? 'Role' : 'Workspace access');
  return (
    <Badge variant="info">
      {ts('{scope} managed by {provider}', { scope, provider: providerLabel })}
    </Badge>
  );
}

function canUsePassword(user: User): boolean {
  return !user.authProvider || user.authProvider === UserAuthProvider.builtin;
}

/**
 * Render the Users management page with a table of accounts and controls for creating, editing, resetting passwords, and deleting users.
 *
 * This component sets the application bar title to "User Management", fetches the user list from the configured API using a stored token, and manages loading and error states. It highlights the current user, formats created/updated timestamps, and exposes per-user actions that open the appropriate modals (create, edit, reset password, delete). Deletion performs an API DELETE request and refreshes the list on success.
 *
 * @returns The Users page component as a JSX.Element
 */
export default function UsersPage() {
  const { ts } = useI18n();
  const config = useConfig();
  const { user: currentUser } = useAuth();
  const isAdmin = useIsAdmin();
  const hasRbac = useHasFeature('rbac');
  const appBarContext = useContext(AppBarContext);
  const [users, setUsers] = useState<User[]>([]);
  const [managedRoleProviders, setManagedRoleProviders] = useState<
    UserAuthProvider[]
  >([]);
  const [managedWorkspaceAccessProviders, setManagedWorkspaceAccessProviders] =
    useState<UserAuthProvider[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const fetchSequence = useRef(0);

  // Modal states
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [editingUser, setEditingUser] = useState<User | null>(null);
  const [resetPasswordUser, setResetPasswordUser] = useState<User | null>(null);
  const [deletingUser, setDeletingUser] = useState<User | null>(null);

  // Set page title
  useEffect(() => {
    appBarContext.setTitle('User Management');
  }, [appBarContext]);

  const fetchUsers = useCallback(async () => {
    const sequence = ++fetchSequence.current;
    setIsLoading(true);
    setError(null);
    setUsers([]);
    setManagedRoleProviders([]);
    setManagedWorkspaceAccessProviders([]);
    setShowCreateModal(false);
    setEditingUser(null);
    setResetPasswordUser(null);
    setDeletingUser(null);
    try {
      const token = localStorage.getItem(TOKEN_KEY);
      const remoteNode = encodeURIComponent(
        appBarContext.selectedRemoteNode || 'local'
      );
      const response = await fetch(
        `${config.apiURL}/users?remoteNode=${remoteNode}`,
        {
          headers: {
            Authorization: `Bearer ${token}`,
          },
        }
      );

      if (!response.ok) {
        throw new Error('Failed to fetch users');
      }

      const data = (await response.json()) as UsersListResponse;
      if (sequence !== fetchSequence.current) {
        return;
      }
      setUsers(data.users || []);
      const roleProviders =
        data.managedRoleProviders ??
        (data.oidcWorkspaceAccessSyncEnabled ? [UserAuthProvider.oidc] : []);
      setManagedRoleProviders(roleProviders);
      setManagedWorkspaceAccessProviders(
        data.managedWorkspaceAccessProviders ?? roleProviders
      );
      setError(null);
    } catch (err) {
      if (sequence !== fetchSequence.current) {
        return;
      }
      setError(err instanceof Error ? err.message : 'Failed to load users');
    } finally {
      if (sequence === fetchSequence.current) {
        setIsLoading(false);
      }
    }
  }, [config.apiURL, appBarContext.selectedRemoteNode]);

  useEffect(() => {
    fetchUsers();
  }, [fetchUsers]);

  const handleDeleteUser = async () => {
    if (!deletingUser) return;

    try {
      const token = localStorage.getItem(TOKEN_KEY);
      const remoteNode = encodeURIComponent(
        appBarContext.selectedRemoteNode || 'local'
      );
      const response = await fetch(
        `${config.apiURL}/users/${deletingUser.id}?remoteNode=${remoteNode}`,
        {
          method: 'DELETE',
          headers: {
            Authorization: `Bearer ${token}`,
          },
        }
      );

      if (!response.ok) {
        const data = await response.json().catch(() => ({}));
        throw new Error(data.message || 'Failed to delete user');
      }

      setError(null); // Clear any previous error on success
      setDeletingUser(null);
      fetchUsers();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete user');
    }
  };

  const handleToggleDisabled = async (user: User) => {
    try {
      const token = localStorage.getItem(TOKEN_KEY);
      const remoteNode = encodeURIComponent(
        appBarContext.selectedRemoteNode || 'local'
      );
      const response = await fetch(
        `${config.apiURL}/users/${user.id}?remoteNode=${remoteNode}`,
        {
          method: 'PATCH',
          headers: {
            'Content-Type': 'application/json',
            Authorization: `Bearer ${token}`,
          },
          body: JSON.stringify({ isDisabled: !user.isDisabled }),
        }
      );

      if (!response.ok) {
        const data = await response.json().catch(() => ({}));
        throw new Error(data.message || 'Failed to update user');
      }

      setError(null);
      fetchUsers();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to update user');
    }
  };

  return (
    <div className="flex flex-col gap-4 max-w-7xl">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-lg font-semibold">
            <I18nText text={'Users'} />
          </h1>
          <p className="text-sm text-muted-foreground">
            <I18nText text={'Manage user accounts and their roles'} />
          </p>
        </div>
        {hasRbac && (
          <Button
            onClick={() => setShowCreateModal(true)}
            size="sm"
            className="h-8"
          >
            <UserPlus className="h-4 w-4 mr-1.5" />
            <I18nText text={'Add User'} />
          </Button>
        )}
      </div>

      {error && (
        <div className="p-3 text-sm text-destructive bg-destructive/10 rounded-md">
          {error}
        </div>
      )}

      {!hasRbac && (
        <div className="flex items-center gap-2 p-3 text-sm text-muted-foreground bg-muted/50 rounded-md">
          <Info className="h-4 w-4 shrink-0" />
          <span>
            <I18nTemplate
              text="User management features (create, edit, delete) require a {licenseLink}. Password reset is available for all admins."
              values={{
                licenseLink: (
                  <Link
                    to="/license"
                    className="text-primary underline underline-offset-2"
                  >
                    <I18nText text="license or trial" />
                  </Link>
                ),
              }}
            />
          </span>
        </div>
      )}

      <div className="card-obsidian overflow-auto min-h-0">
        <Table className="text-xs">
          <TableHeader>
            <TableRow>
              <TableHead className="w-[200px]">
                <I18nText text={'Username'} />
              </TableHead>
              <TableHead className="w-[100px]">
                <I18nText text={'Role'} />
              </TableHead>
              <TableHead className="w-[80px]">
                <I18nText text={'Auth'} />
              </TableHead>
              <TableHead className="w-[80px]">
                <I18nText text={'Status'} />
              </TableHead>
              <TableHead className="w-[150px]">
                <I18nText text={'Created'} />
              </TableHead>
              <TableHead className="w-[150px]">
                <I18nText text={'Updated'} />
              </TableHead>
              <TableHead className="w-[80px]"></TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {isLoading ? (
              <TableRow>
                <TableCell
                  colSpan={7}
                  className="text-center text-muted-foreground py-8"
                >
                  <I18nText text={'Loading users...'} />
                </TableCell>
              </TableRow>
            ) : users.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={7}
                  className="text-center text-muted-foreground py-8"
                >
                  <I18nText text={'No users found'} />
                </TableCell>
              </TableRow>
            ) : (
              users.map((user) => (
                <TableRow
                  key={user.id}
                  className={user.isDisabled ? 'opacity-60' : ''}
                >
                  <TableCell className="font-medium">
                    <div className="flex items-center gap-2">
                      {user.username}
                      {user.id === currentUser?.id && (
                        <span className="text-xs text-muted-foreground">
                          <I18nText text={'(you)'} />
                        </span>
                      )}
                    </div>
                  </TableCell>
                  <TableCell>
                    <span className="text-xs px-1.5 py-0.5 rounded bg-muted text-muted-foreground capitalize">
                      {user.role}
                    </span>
                  </TableCell>
                  <TableCell className="text-sm text-muted-foreground">
                    <AuthProviderBadge
                      user={user}
                      managedRoleProviders={managedRoleProviders}
                      managedWorkspaceAccessProviders={
                        managedWorkspaceAccessProviders
                      }
                    />
                  </TableCell>
                  <TableCell className="text-sm">
                    {user.isDisabled ? (
                      <span className="text-red-600 dark:text-red-400">
                        <I18nText text={'Disabled'} />
                      </span>
                    ) : (
                      <span className="text-muted-foreground">
                        <I18nText text={'Active'} />
                      </span>
                    )}
                  </TableCell>
                  <TableCell className="text-sm text-muted-foreground">
                    {dayjs(user.createdAt).format('MMM D, YYYY HH:mm')}
                  </TableCell>
                  <TableCell className="text-sm text-muted-foreground">
                    {dayjs(user.updatedAt).format('MMM D, YYYY HH:mm')}
                  </TableCell>
                  <TableCell>
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button
                          variant="ghost"
                          size="icon"
                          aria-label={`Actions for ${user.username}`}
                        >
                          <MoreHorizontal className="h-4 w-4" />
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        {hasRbac && (
                          <DropdownMenuItem
                            onClick={() => setEditingUser(user)}
                          >
                            <Pencil className="h-4 w-4 mr-2" />
                            <I18nText text={'Edit'} />
                          </DropdownMenuItem>
                        )}
                        {isAdmin && canUsePassword(user) && (
                          <DropdownMenuItem
                            onClick={() => setResetPasswordUser(user)}
                          >
                            <Key className="h-4 w-4 mr-2" />
                            <I18nText text={'Reset Password'} />
                          </DropdownMenuItem>
                        )}
                        {hasRbac && isAdmin && user.id !== currentUser?.id && (
                          <DropdownMenuItem
                            onClick={() => handleToggleDisabled(user)}
                          >
                            {user.isDisabled ? (
                              <>
                                <UserCheck className="h-4 w-4 mr-2" />
                                <I18nText text={'Enable'} />
                              </>
                            ) : (
                              <>
                                <Ban className="h-4 w-4 mr-2" />
                                <I18nText text={'Disable'} />
                              </>
                            )}
                          </DropdownMenuItem>
                        )}
                        {hasRbac && (
                          <DropdownMenuItem
                            onClick={() => setDeletingUser(user)}
                            className="text-destructive"
                            disabled={user.id === currentUser?.id}
                          >
                            <Trash2 className="h-4 w-4 mr-2" />
                            <I18nText text={'Delete'} />
                          </DropdownMenuItem>
                        )}
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </div>

      {/* Create User Modal */}
      <UserFormModal
        open={showCreateModal}
        onClose={() => setShowCreateModal(false)}
        onSuccess={() => {
          setShowCreateModal(false);
          fetchUsers();
        }}
      />

      {/* Edit User Modal */}
      <UserFormModal
        open={!!editingUser}
        user={editingUser || undefined}
        managedRoleProviders={managedRoleProviders}
        managedWorkspaceAccessProviders={managedWorkspaceAccessProviders}
        onClose={() => setEditingUser(null)}
        onSuccess={() => {
          setEditingUser(null);
          fetchUsers();
        }}
      />

      {/* Reset Password Modal */}
      <ResetPasswordModal
        open={!!resetPasswordUser}
        user={resetPasswordUser || undefined}
        onClose={() => setResetPasswordUser(null)}
      />

      {/* Delete Confirmation */}
      <I18nProps>
        <ConfirmModal
          title="Delete User"
          buttonText="Delete"
          visible={!!deletingUser}
          dismissModal={() => setDeletingUser(null)}
          onSubmit={handleDeleteUser}
        >
          <p>
            {ts(
              'Are you sure you want to delete user "{username}"? This action cannot be undone.',
              { username: deletingUser?.username ?? '' }
            )}
          </p>
        </ConfirmModal>
      </I18nProps>
    </div>
  );
}
