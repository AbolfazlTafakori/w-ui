import { describe, expect, it } from 'vitest'
import { userNumbers } from './users.js'

describe('userNumbers', () => {
  it('numbers a user by their file name, across tunnels, in the order issued', () => {
    const accounts = [
      { id: 5, interfaceId: 2, deviceName: 'user-2' },
      { id: 1, interfaceId: 1, deviceName: 'device-1' },
      { id: 2, interfaceId: 1, deviceName: 'user-2' },
      { id: 4, interfaceId: 2, deviceName: 'device-1' },
      { id: 3, interfaceId: 1, deviceName: 'user-3' },
    ]
    expect(userNumbers(accounts)).toEqual({ 1: 1, 4: 1, 2: 2, 5: 2, 3: 3 })
  })

  it('counts the users that are left when one is gone', () => {
    const accounts = [
      { id: 1, interfaceId: 1, deviceName: 'device-1' },
      { id: 3, interfaceId: 1, deviceName: 'user-3' },
    ]
    expect(userNumbers(accounts)).toEqual({ 1: 1, 3: 2 })
  })
})
