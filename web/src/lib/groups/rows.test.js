import { describe, expect, it } from 'vitest'
import { memberChoices, menuFor } from './rows.js'

describe('a group’s row menu', () => {
  it('keeps its shape on an empty group, with what needs members switched off', () => {
    const empty = menuFor({ clients: 0 })
    const full = menuFor({ clients: 3 })
    expect(empty.map((m) => m.key)).toEqual(full.map((m) => m.key))
    const on = (items) => items.filter((m) => m.key && !m.disabled).map((m) => m.key)
    expect(on(empty)).toEqual(['addClients', 'rename', 'delete'])
    expect(on(full)).toHaveLength(11)
  })

  it('puts a rule before everything that destroys something', () => {
    const items = menuFor({ clients: 1 })
    const rule = items.findIndex((m) => m.divider)
    expect(items.slice(rule + 1).every((m) => m.danger)).toBe(true)
    expect(items.slice(0, rule).some((m) => m.danger)).toBe(false)
  })
})

describe('the members picker', () => {
  const clients = [
    { id: 1, name: 'Alice', groups: ['vip'] },
    { id: 2, name: 'Bob', groups: [] },
    { id: 3, name: 'alina' },
  ]

  it('offers those outside the group to add and those inside to take out', () => {
    expect(memberChoices(clients, 'vip', 'add').map((c) => c.id)).toEqual([2, 3])
    expect(memberChoices(clients, 'vip', 'remove').map((c) => c.id)).toEqual([1])
  })

  it('narrows by name, whatever the case', () => {
    expect(memberChoices(clients, 'other', 'add', ' ALI ').map((c) => c.id)).toEqual([1, 3])
  })
})
