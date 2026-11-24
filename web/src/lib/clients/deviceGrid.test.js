import { describe, it, expect } from 'vitest'
import { deviceGrid } from './deviceGrid.js'

const now = () => new Date().toISOString()
const old = '2026-01-01T00:00:00Z'

describe('deviceGrid', () => {
  const accounts = [
    { id: 1, interfaceId: 10, deviceName: 'Phone', ip: '10.0.0.2', lastHandshake: old },
    { id: 2, interfaceId: 10, deviceName: 'Laptop', ip: '10.0.0.3' },
    { id: 3, interfaceId: 20, deviceName: 'phone', ip: '10.1.0.2', lastHandshake: now() },
    { id: 4, interfaceId: 30, deviceName: 'Laptop', ip: '10.2.0.3', lastHandshake: old },
  ]
  const g = deviceGrid(accounts, [{ id: 10, name: 'wg1', protocol: 'wireguard' }, { id: 20, name: 'ovpn1', protocol: 'openvpn' }], 'wireguard')

  it('makes a row a user, case aside, in the order they appear', () => {
    expect(g.users.map((u) => u.name)).toEqual(['Phone', 'Laptop'])
    expect(g.users[0].accounts.map((a) => a.id)).toEqual([1, 3])
  })

  it('makes a column a server, named where known', () => {
    expect(g.servers).toEqual([
      { id: 10, name: 'wg1', protocol: 'wireguard' },
      { id: 20, name: 'ovpn1', protocol: 'openvpn' },
      { id: 30, name: '#30', protocol: 'wireguard' },
    ])
  })

  it('puts each file in its cell, and none where the user is not', () => {
    expect(g.users[0].cells[20].id).toBe(3)
    expect(g.users[0].cells[30]).toBeUndefined()
    expect(g.users[1].cells[30].id).toBe(4)
  })

  it('is online if any file is, and seen at the latest', () => {
    expect(g.users[0].online).toBe(true)
    expect(g.users[1].online).toBe(false)
    expect(g.users[1].seen).toBe(old)
  })

  it('is empty for no files', () => {
    expect(deviceGrid([], [])).toEqual({ servers: [], users: [] })
  })
})
