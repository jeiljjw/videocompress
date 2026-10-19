import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterEach } from 'vitest'

// Vitest is configured without global test APIs, so Testing Library's automatic
// cleanup is not registered for us. Without this, each render() leaves its DOM
// behind and later queries in the same file match multiple elements.
afterEach(() => {
  cleanup()
})
