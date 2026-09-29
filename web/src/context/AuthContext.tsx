import React, { createContext, useContext, useState, useEffect, useCallback, useRef } from 'react';
import type { SessionUser, LoginRequest, RegisterRequest, AuthResponse } from '@/types/api';
import { api, ApiError, SESSION_EXPIRED_EVENT } from '@/api/client';
import { useLocale } from './LocaleContext';

interface AuthContextType {
  user: SessionUser | null;
  authenticated: boolean;
  loading: boolean;
  error: ApiError | null;
  login: (data: LoginRequest) => Promise<AuthResponse>;
  register: (data: RegisterRequest) => Promise<AuthResponse>;
  logout: () => Promise<void>;
  refreshSession: () => Promise<void>;
  setUser: React.Dispatch<React.SetStateAction<SessionUser | null>>;
}

const AuthContext = createContext<AuthContextType | undefined>(undefined);

export const AuthProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const [user, setUser] = useState<SessionUser | null>(null);
  const [authenticated, setAuthenticated] = useState<boolean>(false);
  const [loading, setLoading] = useState<boolean>(true);
  const [error, setError] = useState<ApiError | null>(null);
  const { setLocale } = useLocale();

  const refreshSession = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);
      const res = await api.getSession();
      if (res.authenticated && res.user) {
        setUser(res.user);
        setAuthenticated(true);
        if (res.user.locale) {
          setLocale(res.user.locale);
        }
      } else {
        setUser(null);
        setAuthenticated(false);
      }
    } catch (err) {
      if (err instanceof ApiError && (err.status === 401 || err.status === 204)) {
        setUser(null);
        setAuthenticated(false);
      } else {
        setError(err instanceof ApiError ? err : new ApiError(500, { code: 'UNKNOWN', messageKey: 'errors.unknown' }));
        setUser(null);
        setAuthenticated(false);
      }
    } finally {
      setLoading(false);
    }
  }, [setLocale]);

  useEffect(() => {
    refreshSession();
  }, [refreshSession]);

  // A 401 from any signed-in request means the session ended; drop to the signed-out state
  // so pages show the login prompt instead of an unexplained error.
  const authenticatedRef = useRef(false);
  authenticatedRef.current = authenticated;
  useEffect(() => {
    const onExpired = () => {
      if (!authenticatedRef.current) return;
      api.setCsrfToken(null);
      setUser(null);
      setAuthenticated(false);
    };
    window.addEventListener(SESSION_EXPIRED_EVENT, onExpired);
    return () => window.removeEventListener(SESSION_EXPIRED_EVENT, onExpired);
  }, []);

  const login = async (data: LoginRequest): Promise<AuthResponse> => {
    setError(null);
    const res = await api.login(data);
    if (res && res.user) {
      setUser(res.user);
      setAuthenticated(true);
      if (res.user.locale) {
        setLocale(res.user.locale);
      }
    }
    return res;
  };

  const register = async (data: RegisterRequest): Promise<AuthResponse> => {
    setError(null);
    const res = await api.register(data);
    if (res && res.user) {
      setUser(res.user);
      setAuthenticated(true);
      if (res.user.locale) {
        setLocale(res.user.locale);
      }
    }
    return res;
  };

  const logout = async () => {
    try {
      await api.logout();
    } finally {
      setUser(null);
      setAuthenticated(false);
    }
  };

  return (
    <AuthContext.Provider
      value={{
        user,
        authenticated,
        loading,
        error,
        login,
        register,
        logout,
        refreshSession,
        setUser,
      }}
    >
      {children}
    </AuthContext.Provider>
  );
};

export function useAuth(): AuthContextType {
  const context = useContext(AuthContext);
  if (!context) {
    throw new Error('useAuth must be used within an AuthProvider');
  }
  return context;
}
