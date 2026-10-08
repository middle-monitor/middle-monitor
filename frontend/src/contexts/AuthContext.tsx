import React, { createContext, useContext, useState, useEffect, useCallback } from 'react';
import { authApi, User, Organization, UserOrganization, AuthTokens } from '../api';
import {
  isDemoMode,
  exitDemoMode,
  demoUser,
  demoOrganization,
  demoUserOrganizations,
  demoWritesEnabled,
  demoAdminEnabled,
} from '../demo/demoMode';

/** Result of a login attempt: either fully authenticated, or a second factor is
 *  still required (the org enforces 2FA and the user is enrolled). */
export type LoginResult =
  | { status: 'success'; organization: Organization }
  | { status: 'mfa_required'; mfaToken: string };

interface AuthContextType {
  user: User | null;
  organization: Organization | null;
  /** Every org the user belongs to (for the org switcher). */
  organizations: UserOrganization[];
  isAuthenticated: boolean;
  /** True as soon as a token exists, before /me answers. Public pages use it to
   *  render the Dashboard CTA on first paint instead of flashing Get Started. */
  hasStoredSession: boolean;
  isLoading: boolean;
  /** True for admin users. Admins can manage the team and billing. */
  isAdmin: boolean;
  /** True for the instance owner (PLATFORM_ADMIN_EMAILS), unrelated to any org's
   *  own admin role. Gates the cross-org /platform-admin page. */
  isPlatformAdmin: boolean;
  /** True when the user may mutate resources (role read_write or admin).
   *  read_only users can read everything but every write is rejected by the API. */
  canWrite: boolean;
  login: (email: string, password: string) => Promise<LoginResult | null>;
  /** Completes a login that required a second factor, using a TOTP or recovery code. */
  completeMfaLogin: (mfaToken: string, code: string) => Promise<{ organization: Organization } | null>;
  register: (email: string, password: string, name: string, organizationName: string, organizationSlug: string) => Promise<{ organization: Organization } | null>;
  /** Activates an invited account by setting its password, then logs the user in. */
  acceptInvite: (token: string, password: string) => Promise<{ organization: Organization } | null>;
  /** Switches the active organization (must be one the user belongs to). */
  switchOrg: (organizationId: number) => Promise<{ organization: Organization } | null>;
  logout: () => void;
  refreshUser: () => Promise<void>;
  /** Exchanges the refresh token for a fresh access token, then reloads the user.
   *  Used after email verification so the new JWT carries email_verified=true. */
  refreshSession: () => Promise<void>;
}

const AuthContext = createContext<AuthContextType | undefined>(undefined);

const TOKEN_KEY = 'auth_token';
const REFRESH_TOKEN_KEY = 'refresh_token';

const missingAuthProvider = () => {
  throw new Error('AuthProvider is missing from the React tree');
};

// Demo mode (/demo): a fake read-only session served without any backend call.
// Exiting reloads the app so every provider re-initializes cleanly.
const demoAuthContext: AuthContextType = {
  user: demoUser,
  organization: demoOrganization,
  organizations: demoUserOrganizations,
  isAuthenticated: true,
  hasStoredSession: true,
  isLoading: false,
  // Not an admin, unless the dev-only seam is on (see demoAdminEnabled).
  isAdmin: demoAdminEnabled(),
  isPlatformAdmin: false,
  // Read-only, unless the dev-only write seam is on (see demoWritesEnabled).
  canWrite: demoWritesEnabled(),
  login: async () => null,
  completeMfaLogin: async () => null,
  register: async () => null,
  acceptInvite: async () => null,
  switchOrg: async () => null,
  logout: () => {
    exitDemoMode();
    window.location.href = '/';
  },
  refreshUser: async () => {},
  refreshSession: async () => {},
};

const unauthenticatedAuthContext: AuthContextType = {
  user: null,
  organization: null,
  organizations: [],
  isAuthenticated: false,
  hasStoredSession: false,
  isLoading: false,
  isAdmin: false,
  isPlatformAdmin: false,
  canWrite: false,
  login: missingAuthProvider,
  completeMfaLogin: missingAuthProvider,
  register: missingAuthProvider,
  acceptInvite: missingAuthProvider,
  switchOrg: missingAuthProvider,
  logout: () => {},
  refreshUser: async () => {},
  refreshSession: async () => {},
};

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [organization, setOrganization] = useState<Organization | null>(null);
  const [organizations, setOrganizations] = useState<UserOrganization[]>([]);
  const [isPlatformAdmin, setIsPlatformAdmin] = useState(false);
  // Only "loading" when there is a token to verify: without one the first render
  // already knows the visitor is anonymous.
  const [isLoading, setIsLoading] = useState(() => !!localStorage.getItem(TOKEN_KEY));

  const saveTokens = (tokens: AuthTokens) => {
    localStorage.setItem(TOKEN_KEY, tokens.access_token);
    if (tokens.refresh_token) {
      localStorage.setItem(REFRESH_TOKEN_KEY, tokens.refresh_token);
    }
  };

  const clearTokens = () => {
    localStorage.removeItem(TOKEN_KEY);
    localStorage.removeItem(REFRESH_TOKEN_KEY);
  };

  // Sets the user together with its organization: a user set alone sends
  // PublicRoute to /organizations/default.
  const fetchCurrentUser = useCallback(async () => {
    try {
      const response = await authApi.getMe();
      setUser(response.data.user);
      setOrganization(response.data.organization);
      setOrganizations(response.data.organizations ?? []);
      setIsPlatformAdmin(response.data.is_platform_admin ?? false);
      return response.data;
    } catch (error) {
      console.error('Failed to fetch current user:', error);
      clearTokens();
      setUser(null);
      setOrganization(null);
      setOrganizations([]);
      setIsPlatformAdmin(false);
      return null;
    }
  }, []);

  const refreshUser = useCallback(async () => {
    await fetchCurrentUser();
  }, [fetchCurrentUser]);

  const refreshSession = useCallback(async () => {
    const refreshToken = localStorage.getItem(REFRESH_TOKEN_KEY);
    if (!refreshToken) return;
    const response = await authApi.refreshToken(refreshToken);
    saveTokens(response.data.tokens);
    await fetchCurrentUser();
  }, [fetchCurrentUser]);

  useEffect(() => {
    if (isDemoMode()) {
      setIsLoading(false);
      return;
    }
    const token = localStorage.getItem(TOKEN_KEY);
    if (token) {
      fetchCurrentUser().finally(() => setIsLoading(false));
    } else {
      setIsLoading(false);
    }
  }, [fetchCurrentUser]);

  const login = async (email: string, password: string): Promise<LoginResult | null> => {
    const response = await authApi.login({ email, password });
    // Org enforces 2FA and the user is enrolled: a second factor is still needed.
    if (response.data.mfa_required && response.data.mfa_token) {
      return { status: 'mfa_required', mfaToken: response.data.mfa_token };
    }
    if (response.data.tokens) saveTokens(response.data.tokens);
    const me = await fetchCurrentUser();
    return me ? { status: 'success', organization: me.organization } : null;
  };

  const completeMfaLogin = async (mfaToken: string, code: string) => {
    const response = await authApi.loginMfa(mfaToken, code);
    saveTokens(response.data.tokens);
    const me = await fetchCurrentUser();
    return me ? { organization: me.organization } : null;
  };

  const register = async (email: string, password: string, name: string, organizationName: string, organizationSlug: string) => {
    const response = await authApi.register({
      email,
      password,
      name,
      organization_name: organizationName,
      organization_slug: organizationSlug,
    });
    saveTokens(response.data.tokens);
    const me = await fetchCurrentUser();
    return me ? { organization: me.organization } : null;
  };

  const acceptInvite = async (token: string, password: string) => {
    const response = await authApi.acceptInvite(token, password);
    saveTokens(response.data.tokens);
    const me = await fetchCurrentUser();
    return me ? { organization: me.organization } : null;
  };

  const switchOrg = async (organizationId: number) => {
    const response = await authApi.switchOrg(organizationId);
    saveTokens(response.data.tokens);
    setUser(response.data.user);
    setOrganization(response.data.organization);
    return { organization: response.data.organization };
  };

  const logout = () => {
    clearTokens();
    setUser(null);
    setOrganization(null);
    setOrganizations([]);
    setIsPlatformAdmin(false);
  };

  if (isDemoMode()) {
    return <AuthContext.Provider value={demoAuthContext}>{children}</AuthContext.Provider>;
  }

  return (
    <AuthContext.Provider
      value={{
        user,
        organization,
        organizations,
        isAuthenticated: !!user,
        hasStoredSession: !!user || isLoading,
        isLoading,
        isAdmin: user?.role === 'admin',
        isPlatformAdmin,
        canWrite: !!user && user.role !== 'read_only',
        login,
        completeMfaLogin,
        register,
        acceptInvite,
        switchOrg,
        logout,
        refreshUser,
        refreshSession,
      }}
    >
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth() {
  const context = useContext(AuthContext);
  if (context === undefined) {
    if (import.meta.env.DEV) {
      console.warn('useAuth was called outside AuthProvider; using anonymous auth context.');
    }
    return unauthenticatedAuthContext;
  }
  return context;
}

export function getAuthToken(): string | null {
  return localStorage.getItem(TOKEN_KEY);
}
