import { afterEach, describe, expect, it } from 'vitest'
import { handsOn } from './actions.js'
import { store } from '../store.js'

// Handing customers on is the owner's and an admin's; a reseller, nobody
// signed in, or an unknown role is never offered it.
describe('who is offered handing customers on', () => {
  afterEach(() => { store.admin = null })
  it('is the owner and an admin', () => {
    store.admin = { id: 1, role: 'owner', managesPanel: true }
    expect(handsOn()).toBe(true)
    store.admin = { id: 2, role: 'admin', managesPanel: false }
    expect(handsOn()).toBe(true)
  })
  it('is never a reseller or nobody', () => {
    store.admin = { id: 3, role: 'reseller', managesPanel: false }
    expect(handsOn()).toBe(false)
    store.admin = null
    expect(handsOn()).toBe(false)
  })
})
