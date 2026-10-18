import { describe, expect, it } from 'vitest'
import { bytes, durationToUnit, isOnline, percent, quotaToUnit, unitToBytes, unitToHours } from './format.js'

describe('sizes', () => {
  it('reads as a person would', () => {
    expect(bytes(0)).toBe('0 B')
    expect(bytes(1536)).toBe('1.50 KB')
    expect(bytes(5 * 1024 ** 3)).toBe('5.00 GB')
  })

  it('a plan goes into the form and comes back the same', () => {
    for (const b of [1024 ** 3 * 50, 1024 ** 4 * 2, 1024 ** 2 * 300]) {
      const { value, unit } = quotaToUnit(b)
      expect(unitToBytes(value, unit)).toBe(b)
    }
    expect(quotaToUnit(0)).toEqual({ value: '', unit: 'GB' })
    expect(unitToBytes('-3', 'GB')).toBe(0)
    expect(unitToBytes('abc', 'GB')).toBe(0)
  })
})

describe('durations', () => {
  it('a plan length goes into the form and comes back the same', () => {
    for (const h of [24 * 30, 24 * 7, 5]) {
      const { value, unit } = durationToUnit(h)
      expect(unitToHours(value, unit)).toBe(h)
    }
    expect(durationToUnit(0)).toEqual({ value: '', unit: 'days' })
  })
})

describe('usage', () => {
  it('never shows more than full, and nothing for no allowance', () => {
    expect(percent(50, 100)).toBe(50)
    expect(percent(300, 100)).toBe(100)
    expect(percent(5, 0)).toBeNull()
  })

  it('is online within three minutes of a handshake, as the server counts', () => {
    expect(isOnline(new Date(Date.now() - 60_000).toISOString())).toBe(true)
    expect(isOnline(new Date(Date.now() - 4 * 60_000).toISOString())).toBe(false)
    expect(isOnline(null)).toBe(false)
    expect(isOnline('not a date')).toBe(false)
  })
})
