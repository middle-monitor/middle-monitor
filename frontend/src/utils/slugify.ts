/**
 * Derives a machine-friendly name from a human-readable label.
 *
 * Mirrors the organization slug logic (see CreateOrganizationView): lowercase,
 * spaces to hyphens, strips anything that is not [a-z0-9-]. Used to auto-fill
 * the technical `name` field from `display_name` on the host and service forms,
 * as long as the user hasn't edited the name manually.
 */
export function slugifyName(value: string): string {
  return value
    .toLowerCase()
    .replace(/\s+/g, '-')
    .replace(/[^a-z0-9-]/g, '')
    .replace(/-+/g, '-')
    .replace(/^-|-$/g, '');
}
