import { useParams } from 'react-router-dom';
import { useAuth } from '../contexts/AuthContext';

/**
 * Returns the organization-scoped path prefix and a helper to build full paths.
 * Uses URL param to stay in sync with the current route.
 * 
 * Example:
 *   orgBase = "/organizations/acme"
 *   orgPath("/hosts") = "/organizations/acme/hosts"
 */
export function useOrgPath() {
  const { orgSlug } = useParams<{ orgSlug: string }>();
  const { organization } = useAuth();
  const slug = orgSlug || organization?.slug || 'default';
  const orgBase = `/organizations/${slug}`;

  const orgPath = (path: string): string => {
    if (path === '/') return orgBase;
    return `${orgBase}${path}`;
  };

  return { orgBase, orgPath, slug };
}
