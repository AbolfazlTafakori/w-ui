import { describe, expect, it } from 'vitest'
import { freeDefaults } from './defaults.js'

const wg = { name: 'wg0', listenPort: 51820, subnet: '10.66.0.0/16', mtu: 1420 }

describe('a new tunnel’s suggested name, subnet and port', () => {
  it('are the protocol’s own on an empty panel', () => {
    expect(freeDefaults('wireguard', wg, [])).toEqual(wg)
  })

  it('move clear of a tunnel that has them', () => {
    const got = freeDefaults('wireguard', wg, [{ name: 'wg0', listenPort: 51820, subnet: '10.66.0.0/16' }])
    expect(got).toEqual({ name: 'wg1', listenPort: 51821, subnet: '10.67.0.0/16', mtu: 1420 })
  })

  it('pass over every name, subnet and port taken, a stealth transport’s port too', () => {
    const got = freeDefaults('wireguard', wg, [
      { name: 'wg0', listenPort: 51820, subnet: '10.66.0.0/16', stealth: { port: 51821 } },
      { name: 'wg1', listenPort: 51822, subnet: '10.67.5.0/24' },
      { name: 'awg1', listenPort: 9000, subnet: '10.68.0.0/16' },
    ])
    expect(got).toMatchObject({ name: 'wg2', listenPort: 51823, subnet: '10.69.0.0/16' })
  })

  it('pass over a subnet that only overlaps', () => {
    const got = freeDefaults('openvpn', { name: 'ovpn0', listenPort: 1194, subnet: '10.8.0.0/16', mtu: 1500 },
      [{ name: 'x', listenPort: 1, subnet: '10.0.0.0/12' }])
    expect(got.subnet).toBe('10.16.0.0/16')
  })

  it('keep the port every built-in client dials', () => {
    const ike = { name: 'ike0', listenPort: 500, subnet: '10.79.0.0/16', mtu: 1400 }
    expect(freeDefaults('ikev2', ike, [{ name: 'ike0', listenPort: 500, subnet: '10.79.0.0/16' }]))
      .toMatchObject({ name: 'ike1', listenPort: 500, subnet: '10.80.0.0/16' })
  })

  it('give a name with no number one', () => {
    expect(freeDefaults('wireguard', { ...wg, name: 'office' }, [{ name: 'office', subnet: '', listenPort: 0 }]).name).toBe('office1')
  })
})
