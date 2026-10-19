import { describe, expect, it } from 'vitest'

describe('tooling', () => {
  it('runs vitest with jsdom', () => {
    expect(typeof document).toBe('object')
  })
})
