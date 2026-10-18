import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, ApiError, apiURL, getToken, saveFile, setToken } from './api.js'

function answer(status, body, headers = {}) {
  return Promise.resolve(
    new Response(body === undefined ? null : typeof body === 'string' ? body : JSON.stringify(body), {
      status,
      headers,
    }),
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
  setToken(null)
})

describe('where calls go', () => {
  it('under the path the panel is served on', () => {
    const base = document.createElement('base')
    base.href = 'https://panel.example/secret/'
    document.head.appendChild(base)
    try {
      expect(apiURL('/api/meta')).toBe('https://panel.example/secret/api/meta')
      expect(apiURL('api/meta')).toBe('https://panel.example/secret/api/meta')
    } finally {
      base.remove()
    }
  })
})

describe('what a failed call says', () => {
  it('names the field the server complained about', async () => {
    vi.stubGlobal('fetch', () => answer(400, { error: 'the name is taken', field: 'name' }))
    const err = await api.post('/api/clients', {}).catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err.message).toBe('the name is taken')
    expect(err.field).toBe('name')
    expect(err.retryable).toBe(false)
  })

  it('tells a throttled caller how long to wait', async () => {
    vi.stubGlobal('fetch', () => answer(429, { error: 'Too many attempts.' }, { 'Retry-After': '30' }))
    const err = await api.post('/api/auth/login', {}).catch((e) => e)
    expect(err.message).toBe('Too many attempts. Try again in 30 seconds.')
    expect(err.retryable).toBe(true)
  })

  it('tells an unreachable panel from a refusal', async () => {
    vi.stubGlobal('fetch', () => Promise.reject(new TypeError('Failed to fetch')))
    const err = await api.get('/api/clients').catch((e) => e)
    expect(err.kind).toBe('network')
    expect(err.retryable).toBe(true)
  })

  it('ends the session once, for every page, when the server says it is over', async () => {
    setToken('abc')
    const seen = vi.fn()
    window.addEventListener('wui:unauthorized', seen)
    vi.stubGlobal('fetch', () => answer(401, { error: 'expired' }))
    const err = await api.get('/api/clients').catch((e) => e)
    window.removeEventListener('wui:unauthorized', seen)
    expect(err.kind).toBe('session')
    expect(seen).toHaveBeenCalledTimes(1)
    expect(getToken()).toBeNull()
  })

  it('a download is refused the same way', async () => {
    setToken('abc')
    vi.stubGlobal('fetch', () => answer(401, ''))
    const err = await saveFile('/api/backups/x.tar.gz', 'x.tar.gz').catch((e) => e)
    expect(err.kind).toBe('session')
    expect(getToken()).toBeNull()
  })
})

describe('what a call sends', () => {
  it('carries the session and the body', async () => {
    setToken('tok')
    const fetch = vi.fn(() => answer(200, { ok: true }))
    vi.stubGlobal('fetch', fetch)
    expect(await api.post('/api/clients', { name: 'ali' })).toEqual({ ok: true })
    const [, init] = fetch.mock.calls[0]
    expect(init.headers.Authorization).toBe('Bearer tok')
    expect(init.credentials).toBe('same-origin')
    expect(JSON.parse(init.body)).toEqual({ name: 'ali' })
  })
})
