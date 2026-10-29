/** The per-job override editor of Profile → Notifications, opened on this job. */
export function personalOverridesPath(jobId: string): string {
  return `/profile/notifications?job=${encodeURIComponent(jobId)}`;
}
