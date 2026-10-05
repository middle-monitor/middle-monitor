import { useMemo } from 'react';
import { useParams } from 'react-router-dom';
import { useAuth } from '../contexts/AuthContext';
import { createOrgApi } from '../api';
import { isDemoMode } from '../demo/demoMode';
import { createDemoApi } from '../demo/demoApi';
import { orgQueryScope } from '../queryClient';

function useOrgSlug(): string {
  const { orgSlug } = useParams<{ orgSlug: string }>();
  const { organization } = useAuth();
  return orgSlug || organization?.slug || 'default';
}

/**
 * Returns an org-scoped API client based on the org slug from the URL.
 * Uses URL param to stay in sync with the current route and avoid 403s.
 * Fallback to organization.slug when outside org routes.
 * In demo mode (/demo) it returns the in-memory demo client instead, so no
 * request ever reaches the backend.
 */
export function useOrgApi() {
  const slug = useOrgSlug();

  return useMemo(() => (isDemoMode() ? createDemoApi() : createOrgApi(slug)), [slug]);
}

/**
 * Query key prefix for the current org. Every useQuery in an org view starts
 * with it, so switching org switches cache entry instead of showing the
 * previous org's data.
 */
export function useOrgQueryScope(): string {
  const slug = useOrgSlug();

  return orgQueryScope(slug, isDemoMode());
}
