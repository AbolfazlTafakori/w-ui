// Thin wrapper over fetch that carries the session token and turns API errors
// into thrown Errors with the server's message, so views can show what actually
// went wrong instead of "request failed".

const TOKEN_KEY = 'wui.token'

export function getToken() {
  try {
    return localStorage.getItem(TOKEN_KEY)
  } catch {
    return null
  }
}

export function setToken(token) {
  try {
    if (token) localStorage.setItem(TOKEN_KEY, token)
    else localStorage.removeItem(TOKEN_KEY)
  } catch {
    /* private browsing: the session simply does not survive a reload */
  }
}

export class ApiError extends Error {
  constructor(message, status, kind = 'server', field = '') {
    super(message)
    this.name = 'ApiError'
    this.status = status
    // Which input the server was complaining about, when it said. Lets a form
    // put the message under the field it belongs to instead of in a toast that
    // names nothing.
    this.field = field
    // kind separates the three things a caller may want to do differently:
    // 'network' — the panel could not be reached at all, so retrying may work
    // 'timeout' — it was reached and did not answer
    // 'server'  — it answered, and said no
    this.kind = kind
  }

  get retryable() {
    return this.kind !== 'server' || this.status >= 500 || this.status === 429
  }
}

// requestTimeout bounds every call. Without one a request to a server that
// accepted the connection and then stopped answering hangs until the browser
// gives up, which is minutes — and the page sits on a spinner the whole time
// with no way to tell that anything is wrong.
const requestTimeout = 30_000

// Work the operator is waiting for.
//
// The bar is driven from here rather than from the router, because a route
// change is usually instant and it is the data behind it that takes time. It is
// also held back briefly: a request that finishes in 80ms would otherwise flash
// a bar, and a flash reads as a glitch rather than as progress.
let inFlight = 0
let showTimer = null
const SHOW_AFTER = 160

function started(background) {
  if (background) return
  inFlight++
  if (inFlight === 1 && !showTimer) {
    showTimer = setTimeout(() => {
      showTimer = null
      if (inFlight > 0) window.dispatchEvent(new CustomEvent('wui:busy', { detail: true }))
    }, SHOW_AFTER)
  }
}

function finished(background) {
  if (background) return
  inFlight = Math.max(0, inFlight - 1)
  if (inFlight === 0) {
    if (showTimer) {
      clearTimeout(showTimer)
      showTimer = null
    }
    window.dispatchEvent(new CustomEvent('wui:busy', { detail: false }))
  }
}

async function request(method, path, body, opts = {}) {
  // Polling refreshes must not light the bar: a page that re-reads itself every
  // few seconds would keep it on permanently, and it would stop meaning
  // anything.
  const background = !!opts.background
  started(background)
  try {
    return await send(method, path, body)
  } finally {
    finished(background)
  }
}

async function send(method, path, body) {
  const headers = {}
  const token = getToken()
  if (token) headers.Authorization = `Bearer ${token}`
  if (body !== undefined) headers['Content-Type'] = 'application/json'

  const abort = new AbortController()
  const timer = setTimeout(() => abort.abort(), requestTimeout)

  let res
  try {
    res = await fetch(path, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: abort.signal,
    })
  } catch (err) {
    // fetch rejects for exactly two reasons worth telling apart, and its own
    // message ("Failed to fetch") says neither.
    if (err?.name === 'AbortError') {
      throw new ApiError(
        'The server did not answer in time. It may be busy or restarting.',
        0,
        'timeout',
      )
    }
    throw new ApiError(
      'Could not reach the panel. Check that it is running and that your connection is up.',
      0,
      'network',
    )
  } finally {
    clearTimeout(timer)
  }

  if (res.status === 401 && !path.endsWith('/auth/login')) {
    // The session is gone. Every request in flight is about to fail for this
    // one reason, so it is announced once here rather than by each page that
    // happens to catch it first — which is how the operator ended up reading
    // whichever message won the race.
    setToken(null)
    window.dispatchEvent(new CustomEvent('wui:unauthorized'))
    throw new ApiError('', 401, 'session')
  }

  if (res.status === 204) return null

  const text = await res.text()
  let payload = null
  if (text) {
    try {
      payload = JSON.parse(text)
    } catch {
      payload = { error: text }
    }
  }

  if (!res.ok) {
    // A throttled caller is told how long to wait rather than left to guess.
    if (res.status === 429) {
      const after = Number(res.headers.get('Retry-After'))
      const wait = Number.isFinite(after) && after > 0 ? ` Try again in ${after} seconds.` : ''
      throw new ApiError((payload?.error || 'Too many attempts.') + wait, 429)
    }
    if (res.status >= 500) {
      throw new ApiError(
        payload?.error || 'The panel hit an error handling that. Its log will say more.',
        res.status,
      )
    }
    throw new ApiError(
      payload?.error || `Request failed (${res.status})`,
      res.status,
      'server',
      payload?.field || '',
    )
  }
  return payload
}

export const api = {
  // opts.background marks a poll: it does work but the operator is not waiting
  // on it, so it must not light the progress bar.
  get: (path, opts) => request('GET', path, undefined, opts),
  post: (path, body) => request('POST', path, body ?? {}),
  patch: (path, body) => request('PATCH', path, body ?? {}),
  put: (path, body) => request('PUT', path, body ?? {}),
  del: (path) => request('DELETE', path),

  login: (username, password, code) =>
    request('POST', '/api/auth/login', { username, password, code: code || '' }),
  me: () => request('GET', '/api/auth/me'),
  updateMe: (input) => request('PATCH', '/api/auth/me', input),
  changePassword: (currentPassword, newPassword) =>
    request('POST', '/api/auth/password', { currentPassword, newPassword }),
  system: () => request('GET', '/api/system'),
  meta: () => request('GET', '/api/meta'),
  messages: (locale) => request('GET', `/api/i18n/${locale}`),
  overview: (opts) => request('GET', '/api/overview', undefined, opts),
  fullOverview: (opts) => request('GET', '/api/overview/full', undefined, opts),

  interfaces: (opts) => request('GET', '/api/interfaces', undefined, opts),
  createInterface: (input) => request('POST', '/api/interfaces', input),
  updateInterface: (id, input) => request('PATCH', `/api/interfaces/${id}`, input),
  deleteInterface: (id) => request('DELETE', `/api/interfaces/${id}`),

  clients: (params = {}, opts) => {
    const q = new URLSearchParams()
    for (const [k, v] of Object.entries(params)) {
      if (v !== '' && v != null) q.set(k, v)
    }
    const qs = q.toString()
    return request('GET', `/api/clients${qs ? `?${qs}` : ''}`, undefined, opts)
  },
  client: (id) => request('GET', `/api/clients/${id}`),
  createClient: (input) => request('POST', '/api/clients', input),
  updateClient: (id, input) => request('PATCH', `/api/clients/${id}`, input),
  deleteClient: (id) => request('DELETE', `/api/clients/${id}`),
  resetTraffic: (id) => request('POST', `/api/clients/${id}/reset`, {}),
  bulkClients: (action, ids) => request('POST', '/api/clients/bulk', { action, ids }),
  adjustClients: (input) => request('POST', '/api/clients/adjust', input),
  resetAllTraffic: () => request('POST', '/api/clients/reset-all', {}),
  purgeClients: (status) => request('POST', '/api/clients/purge', { status }),
  createBatch: (input) => request('POST', '/api/clients/batch', input),
  exportUrl: () => '/api/clients/export',
  addDevice: (id, name) => request('POST', `/api/clients/${id}/devices`, { name }),

  groups: (opts) => request('GET', '/api/groups', undefined, opts),
  groupNames: () => request('GET', '/api/groups/names'),
  renameGroup: (from, to) => request('POST', '/api/groups/rename', { from, to }),
  assignGroup: (group, ids) => request('POST', '/api/groups/assign', { group, ids }),
  groupAction: (op) => request('POST', '/api/groups/action', op),

  removeDevice: (id) => request('DELETE', `/api/devices/${id}`),
  profile: (id) => request('GET', `/api/devices/${id}/profile`),
  profileDownloadUrl: (id) => `/api/devices/${id}/profile?download=1`,
}
