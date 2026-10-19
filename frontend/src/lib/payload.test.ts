import { describe, expect, it } from 'vitest'
import { jobListPayload, objectPayload } from './payload'
import type { Job } from './api'

const job = (id: string): Job => ({
  id,
  input: `C:\\videos\\${id}.mp4`,
  outputs: null,
  status: 'queued',
  stage: '',
  percent: 0,
  message: '',
  addedAt: '',
  durationMs: 0,
})

describe('jobListPayload', () => {
  it('accepts the list wails v2 delivers (callback(...args))', () => {
    const list = [job('a'), job('b')]
    expect(jobListPayload(list)).toEqual(list)
  })

  it('accepts an empty queue', () => {
    expect(jobListPayload([])).toEqual([])
  })

  it('accepts a payload wrapped in an argument array', () => {
    const list = [job('a')]
    expect(jobListPayload([list])).toEqual(list)
  })

  it('accepts a single job object', () => {
    expect(jobListPayload(job('a'))).toEqual([job('a')])
  })

  it('rejects payloads that are not jobs', () => {
    expect(jobListPayload(undefined)).toBeUndefined()
    expect(jobListPayload(null)).toBeUndefined()
    expect(jobListPayload('nope')).toBeUndefined()
    expect(jobListPayload(42)).toBeUndefined()
    expect(jobListPayload([1, 2])).toBeUndefined()
    expect(jobListPayload([[1, 2]])).toBeUndefined()
  })
})

describe('objectPayload', () => {
  it('accepts the object wails v2 delivers', () => {
    expect(objectPayload<{ message: string }>({ message: 'hi' })).toEqual({ message: 'hi' })
  })

  it('accepts a wrapped object but not arrays of scalars', () => {
    expect(objectPayload<{ message: string }>([{ message: 'hi' }])).toEqual({ message: 'hi' })
    expect(objectPayload([])).toBeUndefined()
    expect(objectPayload([1, 2])).toBeUndefined()
    expect(objectPayload('nope')).toBeUndefined()
    expect(objectPayload(null)).toBeUndefined()
  })
})
