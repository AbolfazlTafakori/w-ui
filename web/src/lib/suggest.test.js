import { describe, expect, it } from 'vitest'
import { freeName, nextName, nextSubnet } from './suggest.js'

describe('a copy of a tunnel', () => {
  it('is named one along', () => {
    expect(nextName('wg0')).toBe('wg1')
    expect(nextName('ovpn443')).toBe('ovpn444')
    expect(nextName('office')).toBe('office-2')
  })

  it('skips names already taken', () => {
    expect(freeName('wg0', ['wg0', 'wg1', 'wg2'])).toBe('wg3')
    expect(freeName('wg0', [])).toBe('wg1')
  })

  it('moves its range one along in the third octet', () => {
    expect(nextSubnet('10.66.0.0/24')).toBe('10.66.1.0/24')
    expect(nextSubnet('10.66.255.0/24')).toBe('10.66.0.0/24')
    expect(nextSubnet('not a range')).toBe('not a range')
  })
})
