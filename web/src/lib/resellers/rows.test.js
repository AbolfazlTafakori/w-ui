import { describe, expect, it } from 'vitest'
import { DAY, bucket, customersTag, shownRows, standing, summarize, termTag, trafficLeftTag } from './rows.js'

const NOW = Date.parse('2026-10-18T12:00:00Z')
const at = (days) => new Date(NOW + days * DAY).toISOString()
const GB = 1024 ** 3
const reseller = (over) => ({ id: 1, username: 'r', role: 'reseller', enabled: true, usedBytes: 0, quotaBytes: 0, ...over })

describe('where a reseller stands', () => {
  it('puts the owner’s switch first, then the date, then the traffic', () => {
    expect(standing(reseller({ enabled: false, expiresAt: at(-1) }), NOW).key).toBe('off')
    expect(standing(reseller({ expiresAt: at(-1), quotaBytes: GB, usedBytes: GB }), NOW).key).toBe('ended')
    expect(standing(reseller({ expiresAt: at(30), quotaBytes: GB, usedBytes: GB }), NOW).key).toBe('spent')
    expect(standing(reseller({ durationDays: 30 }), NOW).key).toBe('hold')
    expect(standing(reseller({ expiresAt: at(3) }), NOW).key).toBe('soon')
    expect(standing(reseller({ quotaBytes: 100, usedBytes: 90 }), NOW).key).toBe('soon')
    expect(standing(reseller({ expiresAt: at(30) }), NOW).key).toBe('active')
  })

  it('gives an administrator no ceiling to run into', () => {
    expect(standing({ role: 'admin', enabled: true, expiresAt: at(-5) }, NOW).key).toBe('active')
    expect(bucket({ role: 'admin', enabled: false }, NOW)).toBe('stopped')
  })

  it('folds the three ways of being stopped into one filter', () => {
    expect(bucket(reseller({ enabled: false }), NOW)).toBe('stopped')
    expect(bucket(reseller({ expiresAt: at(-1) }), NOW)).toBe('stopped')
    expect(bucket(reseller({ quotaBytes: 1, usedBytes: 2 }), NOW)).toBe('stopped')
    expect(bucket(reseller({ durationDays: 10 }), NOW)).toBe('hold')
  })
})

describe('what is left', () => {
  it('colours traffic as the customer list does', () => {
    expect(trafficLeftTag(reseller({})).label).toBe('∞')
    expect(trafficLeftTag(reseller({ quotaBytes: 100, usedBytes: 10 })).color).toBe('green')
    expect(trafficLeftTag(reseller({ quotaBytes: 100, usedBytes: 90 })).color).toBe('orange')
    expect(trafficLeftTag(reseller({ quotaBytes: 100, usedBytes: 120 })).color).toBe('red')
  })

  it('counts days from now and warns in the last week', () => {
    expect(termTag(reseller({}), NOW).label).toBe('∞')
    expect(termTag(reseller({ expiresAt: at(3) }), NOW).color).toBe('orange')
    expect(termTag(reseller({ expiresAt: at(30) }), NOW).color).toBe('green')
    expect(termTag(reseller({ expiresAt: at(-1) }), NOW).color).toBe('red')
    expect(termTag(reseller({ durationDays: 30 }), NOW).color).toBe('blue')
  })

  it('shows users against the limit, orange when it is full', () => {
    expect(customersTag(reseller({ users: 3 })).color).toBe('')
    expect(customersTag(reseller({ users: 5, clientLimit: 5 })).color).toBe('orange')
  })
})

describe('the list', () => {
  const rows = [
    reseller({ id: 1, username: 'carol', users: 2, usedBytes: 5, interfaceIds: [1], createdAt: '2026-01-01' }),
    reseller({ id: 2, username: 'alice', users: 9, usedBytes: 1, expiresAt: at(3), note: 'north', createdAt: '2026-03-01' }),
    reseller({ id: 3, username: 'bob', users: 2, usedBytes: 9, enabled: false, interfaceIds: [1, 2], createdAt: '2026-02-01' }),
    { id: 4, username: 'dave', role: 'admin', enabled: true },
  ]
  const names = (list) => list.map((a) => a.username)

  it('searches name, note and group, and filters by status and server', () => {
    expect(names(shownRows(rows, { search: 'NOR' }, NOW))).toEqual(['alice'])
    expect(names(shownRows(rows, { status: 'stopped' }, NOW))).toEqual(['bob'])
    expect(names(shownRows(rows, { server: 2 }, NOW))).toEqual(['bob'])
  })

  it('sorts, with the name breaking ties', () => {
    expect(names(shownRows(rows, { sort: 'name' }, NOW))).toEqual(['alice', 'bob', 'carol', 'dave'])
    expect(names(shownRows(rows, { sort: 'customers' }, NOW)).slice(0, 3)).toEqual(['alice', 'bob', 'carol'])
    expect(names(shownRows(rows, { sort: 'traffic' }, NOW)).slice(0, 3)).toEqual(['bob', 'carol', 'alice'])
    expect(names(shownRows(rows, { sort: 'timeLeft' }, NOW))[0]).toBe('alice')
    expect(names(shownRows(rows, { sort: 'newest' }, NOW)).slice(0, 3)).toEqual(['alice', 'bob', 'carol'])
  })

  it('totals resellers alone, but counts everyone', () => {
    const s = summarize(rows, NOW)
    expect(s.total).toBe(4)
    expect(s.active).toBe(1)
    expect(s.soon).toBe(1)
    expect(s.stopped).toBe(1)
    expect(s.traffic).toBe(15)
  })
})
