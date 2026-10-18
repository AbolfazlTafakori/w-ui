import { describe, expect, it } from 'vitest'
import { parseAdvanced } from './xrayInbound.js'

describe('the advanced JSON field', () => {
  it('takes an object, nothing, or refuses', () => {
    expect(parseAdvanced('')).toEqual({})
    expect(parseAdvanced(' {"a":1} ')).toEqual({ a: 1 })
    expect(() => parseAdvanced('[1]')).toThrow()
    expect(() => parseAdvanced('{nope')).toThrow()
  })
})
