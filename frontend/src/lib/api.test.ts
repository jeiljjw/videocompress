import { afterEach, describe, expect, it, vi } from 'vitest'
import { app, on, requireApp } from './api'

afterEach(() => {
  delete (window as unknown as { go?: unknown }).go
  delete (window as unknown as { runtime?: unknown }).runtime
})

describe('app()', () => {
  it('returns null outside the desktop app', () => {
    expect(app()).toBeNull()
  })

  it('returns the Go binding when present', () => {
    const binding = { GetJobs: vi.fn() }
    ;(window as unknown as { go: unknown }).go = { main: { App: binding } }
    expect(app()).toBe(binding)
  })
})

describe('requireApp()', () => {
  it('throws a helpful error when the binding is missing', () => {
    expect(() => requireApp()).toThrowError(/데스크톱/)
  })
})

describe('on()', () => {
  it('is a no-op without the wails runtime', () => {
    const off = on('queue:update', () => {})
    expect(typeof off).toBe('function')
    expect(() => off()).not.toThrow()
  })

  it('subscribes and unsubscribes through the runtime', () => {
    const cancel = vi.fn()
    const EventsOn = vi.fn(() => cancel)
    ;(window as unknown as { runtime: unknown }).runtime = { EventsOn, EventsOff: vi.fn() }
    const cb = vi.fn()
    const off = on('queue:update', cb)
    expect(EventsOn).toHaveBeenCalledWith('queue:update', cb)
    off()
    expect(cancel).toHaveBeenCalled()
  })
})
