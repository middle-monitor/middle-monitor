/**
 * Extracts a user-facing message from a failed API call.
 *
 * Covers both sources a view can get: the backend JSON error (err.response.data.error,
 * see backend/api/respond.go) and a client-side rejection carrying its own message —
 * demo mode rejects every write with "This demo is read-only." and that text is the
 * only thing telling the visitor why nothing happened.
 */
export function apiErrorMessage(err: unknown, fallback: string): string {
  const e = err as { response?: { data?: { error?: string } }; message?: string };
  return e?.response?.data?.error || e?.message || fallback;
}
