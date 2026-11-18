import { describe, expect, it } from 'vitest'
import { capacityMessage, cardOf, unitsFor } from './capacity.js'

const t = (key, vars) => key + (vars ? ' ' + JSON.stringify(vars) : '')

describe('capacityMessage', () => {
  it('says a consumable refusal with its numbers', () => {
    expect(capacityMessage('No users left: this needs 3 users and you have 1 left; ask for more to be added', t))
      .toBe('cap.err.consumable {"need":3,"left":1}')
  })
  it('says an open refusal with its numbers', () => {
    expect(capacityMessage('This needs 2 users at once and 0 of your 100 are free; remove or let a customer end, or ask for more', t))
      .toBe('cap.err.open {"need":2,"free":0,"total":100}')
  })
  it('says the token refusals', () => {
    expect(capacityMessage('You have no tokens left to issue; ask the owner for more', t)).toBe('cap.err.tokens')
    expect(capacityMessage("a reseller's API tokens are issued by their admin", t)).toBe('cap.err.resellerTokens')
  })
  it('leaves anything else as it came', () => {
    expect(capacityMessage('name is taken', t)).toBe('name is taken')
    expect(capacityMessage(undefined, t)).toBe('')
  })
})

describe('cardOf', () => {
  it('reads open seats as held of given', () => {
    const c = cardOf({ userCapacity: { mode: 'open', granted: 100, used: 52, handed: 10, free: 38 } })
    expect(c.users).toMatchObject({ mode: 'open', given: 100, used: 52, handed: 10, free: 38 })
    expect(c.users.fill).toBeCloseTo(0.62)
    expect(c.tokens).toBeNull()
  })
  it('has no token card with nothing given and nothing held', () => {
    expect(cardOf({ tokenCapacity: { granted: 0, used: 0, held: 0 } }).tokens).toBeNull()
    expect(cardOf({ tokenCapacity: { granted: 0, used: 0, held: 2 } }).tokens).not.toBeNull()
  })
  it('never shows more than full', () => {
    expect(cardOf({ userCapacity: { mode: 'open', granted: 10, used: 12, handed: 0, free: -2 } }).users)
      .toMatchObject({ free: 0, fill: 1 })
  })
})

describe('unitsFor', () => {
  const open = { userCapacity: { mode: 'open' } }
  const units = { userCapacity: { mode: 'consumable' } }
  it('takes nothing without a ceiling', () => {
    expect(unitsFor({}, { kind: 'create', users: 3 })).toBe(0)
  })
  it('creating takes a unit or a seat per user', () => {
    expect(unitsFor(open, { kind: 'create', users: 2 })).toBe(2)
    expect(unitsFor(units, { kind: 'create', users: 2 })).toBe(2)
  })
  it('extending a live customer is free with seats, spent with units', () => {
    expect(unitsFor(open, { kind: 'extend', users: 2 })).toBe(0)
    expect(unitsFor(open, { kind: 'extend', users: 2, ended: true })).toBe(2)
    expect(unitsFor(units, { kind: 'extend', users: 2 })).toBe(2)
  })
  it('growing takes the users added', () => {
    expect(unitsFor(units, { kind: 'grow', users: 5, from: 2 })).toBe(3)
    expect(unitsFor(open, { kind: 'grow', users: 1, from: 2 })).toBe(0)
  })
})
