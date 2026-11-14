import { ReactNode } from 'react';
import { Navigate, useLocation } from 'react-router-dom';
import { useAuth, hasRole, type UserRole } from '@/contexts/AuthContext';
import { useConfig } from '@/contexts/ConfigContext';

type ProtectedRouteProps = {
  children: ReactNode;
  requiredRole?: UserRole;
};

export function ProtectedRoute({ children, requiredRole }: ProtectedRouteProps) {
  const config = useConfig();
  const { isAuthenticated, isLoading, setupRequired, user } = useAuth();
  const location = useLocation();

  if (config.authMode !== 'builtin') {
    return <>{children}</>;
  }

  if (isLoading) {
    return null;
  }

  if (setupRequired && !isAuthenticated) {
    return <Navigate to="/setup" state={{ from: location }} replace />;
  }

  if (!isAuthenticated) {
    return <Navigate to="/login" state={{ from: location }} replace />;
  }

  if (requiredRole && user && !hasRole(user.role, requiredRole)) {
    return <Navigate to="/" replace />;
  }

  return <>{children}</>;
}
