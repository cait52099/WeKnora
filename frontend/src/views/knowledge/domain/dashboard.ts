export function dashboardQuery(status: string, search: string, cursors: string[]) {
  return new URLSearchParams({ status, search, before: cursors[cursors.length - 1] || '' }).toString()
}

export function moveCursor(cursors: string[], forward: boolean, next: string): string[] {
  if (forward && next) return [...cursors, next]
  if (!forward && cursors.length > 1) return cursors.slice(0, -1)
  return cursors
}

export function failedPageJobs<T extends { status: string }>(jobs: T[]): T[] {
  return jobs.filter(job => job.status === 'failed' || job.status === 'capture_failed').slice(0, 50)
}
