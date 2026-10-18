import { describe, expect, it } from 'vitest'
import { DEPLETING_AT, barColor, expiryTag, remainingTag, statusTag } from './clientRow.js'

const GB = 1024 ** 3
const customer = (over = {}) => ({ status: 'active', quotaBytes: 10 * GB, usedBytes: GB, onlineNow: 0, ...over })

describe('a customer row', () => {
  it('says why a plan ended', () => {
    expect(statusTag(customer({ status: 'exhausted' }))).toMatchObject({ color: 'red', label: 'status.exhausted' })
    expect(statusTag(customer({ status: 'expired' }))).toMatchObject({ color: 'red', label: 'status.expired' })
  })

  it('shows a paused reseller over the customer’s own state', () => {
    expect(statusTag(customer({ ownerPaused: 'term ended', onlineNow: 2 }))).toMatchObject({ color: 'red', label: 'client.paused' })
  })

  it('is online while it moves traffic, unless switched off', () => {
    expect(statusTag(customer({ onlineNow: 1 }))).toMatchObject({ color: 'green', label: 'status.online' })
    expect(statusTag(customer({ status: 'disabled', onlineNow: 1 }))).toMatchObject({ color: 'grey', label: 'status.disabled' })
  })

  it('is on hold before the first connection of a plan that starts then', () => {
    expect(statusTag(customer({ startOnFirstUse: true, durationDays: 30 }))).toMatchObject({ color: 'blue' })
    expect(expiryTag(customer({ startOnFirstUse: true, durationDays: 30 }))).toMatchObject({ color: 'blue', label: '30d' })
  })

  it('runs low at the server’s own line', () => {
    const low = customer({ usedBytes: (DEPLETING_AT / 100) * 10 * GB })
    expect(DEPLETING_AT).toBe(80)
    expect(statusTag(low)).toMatchObject({ color: 'orange' })
    expect(remainingTag(low).color).toBe('orange')
    expect(barColor(low)).toBe('var(--warn)')
    expect(remainingTag(customer()).color).toBe('green')
  })

  it('is unlimited with no allowance and no date', () => {
    expect(remainingTag(customer({ quotaBytes: 0 }))).toEqual({ color: 'purple', label: '∞' })
    expect(expiryTag(customer())).toEqual({ color: 'purple', label: '∞' })
  })

  it('turns orange three days before the end, and red after it', () => {
    const in2d = new Date(Date.now() + 2 * 86400e3).toISOString()
    const ago = new Date(Date.now() - 86400e3).toISOString()
    const in9d = new Date(Date.now() + 9 * 86400e3).toISOString()
    expect(expiryTag(customer({ expiresAt: in2d })).color).toBe('orange')
    expect(expiryTag(customer({ expiresAt: ago })).color).toBe('red')
    expect(expiryTag(customer({ expiresAt: in9d })).color).toBe('green')
  })
})
