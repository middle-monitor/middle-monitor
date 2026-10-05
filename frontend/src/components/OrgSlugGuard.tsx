import { Navigate, useParams } from 'react-router-dom';
import { useAuth } from '../contexts/AuthContext';

/**
 * Redirects to the user's organization when the URL orgSlug doesn't match.
 * Prevents 403s from API calls using wrong slug.
 */
export default function OrgSlugGuard({ children }: { children: React.ReactNode }) {
  const { orgSlug } = useParams<{ orgSlug: string }>();
  const { organization } = useAuth();

  if (!organization) return <>{children}</>;
  if (orgSlug && orgSlug !== organization.slug) {
    return <Navigate to={`/organizations/${organization.slug}`} replace />;
  }
  return <>{children}</>;
}
