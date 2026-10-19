import type { Job } from './api'

// Wails v2 dispatches an event to the JS callback with `callback.apply(null, data)`
// (see internal/frontend/runtime/desktop/events.js), so the first argument is the
// value the Go side emitted. Some runtimes instead pass the argument array
// itself. These helpers accept both shapes, and never mistake a real array
// payload (the job list) for the wrapper.

function isJob(value: unknown): value is Job {
  return typeof value === 'object' && value !== null && typeof (value as Job).id === 'string'
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

/**
 * jobListPayload decodes a `queue:update` payload. It returns undefined when the
 * payload cannot be a job list, so a malformed event cannot blank the UI.
 */
export function jobListPayload(data: unknown): Job[] | undefined {
  if (Array.isArray(data)) {
    if (data.length === 1 && Array.isArray(data[0])) {
      const inner = data[0]
      return inner.length === 0 || isJob(inner[0]) ? (inner as Job[]) : undefined
    }
    return data.length === 0 || isJob(data[0]) ? (data as Job[]) : undefined
  }
  return isJob(data) ? [data] : undefined
}

/**
 * objectPayload decodes an event carrying a single JSON object (FFmpeg status,
 * notices). Arrays of exactly one object are accepted for wrapped dispatches.
 */
export function objectPayload<T>(data: unknown): T | undefined {
  if (Array.isArray(data)) {
    return data.length === 1 && isRecord(data[0]) ? (data[0] as T) : undefined
  }
  return isRecord(data) ? (data as T) : undefined
}
