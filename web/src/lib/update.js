// Updating the panel from its own page: whether there is a newer release,
// installing it, and following the panel through its restart onto the new
// build. Shared by the overview's header -- the version, the "updating" badge
// -- and its update dialog, which reads the same state.
import { computed, inject, onBeforeUnmount, onMounted, provide, ref, watch } from 'vue'
import { api } from './api.js'
import { bytes } from './format.js'
import { t, notify } from './store.js'

const UPDATE = Symbol('update')

// useUpdate starts following the panel's updates for a page, and hands the
// state to the components under it. version is the version the page reads
// every few seconds anyway, which says when the new build is back.
export function useUpdate(version) {
  // Whether there is a newer release, and whether this build could install it.
  //
  // A build with no signing key cannot, and says so instead of offering a button
  // that fails: refusing to install something nobody vouched for is the point,
  // not an accident.
  const update = ref(null)
  const updateBusy = ref(false)

  async function loadUpdate(fresh = false) {
    try {
      update.value = await api.get(fresh ? '/api/system/update?fresh=1' : '/api/system/update')
    } catch {
      // Quiet. The release list being unreachable is not something to interrupt
      // an operator looking at their own server for.
      update.value = null
    }
  }

  function openUpdate() {
    loadUpdate(true)
    // An install started before this page was loaded -- in another tab, or
    // before a reload -- is picked up where it is rather than offered again.
    if (!installing.value) followInstall(true)
    updateOpen.value = true
  }

  const updateOpen = ref(false)

  // An install under way. The panel downloads the release in the background and
  // answers at once, because fetching it can outlast a request; the page then
  // asks every second how far it has got. Once the new binary is in place the
  // panel restarts, is briefly not there at all, and comes back reporting the
  // new version -- which is when the page reloads onto it.
  //
  // installing: { stage, from, to, received, total, error } while followed.
  const installing = ref(null)
  let followTimer = null
  let followStarted = 0
  // How long a panel may take to come back before the page stops waiting and
  // says so. A restart takes seconds; this is for one that did not happen.
  const COME_BACK_MS = 3 * 60 * 1000

  async function applyUpdate() {
    updateBusy.value = true
    try {
      const res = await api.post('/api/system/update')
      if (res?.started) {
        installing.value = { stage: 'downloading', from: res.from, to: res.to, received: 0, total: 0 }
        followInstall()
      } else {
        notify(res?.notice || t('update.upToDate'), 'ok')
        updateOpen.value = false
      }
    } catch (e) {
      notify(e.message, 'error')
    } finally {
      updateBusy.value = false
    }
  }

  // followInstall asks how the install is going until it ends. probe is a
  // single look on opening the dialog: it follows only if one is under way.
  async function followInstall(probe = false) {
    clearTimeout(followTimer)
    if (!probe && !followStarted) followStarted = Date.now()
    let res = null
    try {
      res = await api.get('/api/system/update/progress')
    } catch {
      // Not answering is expected once the install is in place: the panel is
      // restarting. Before that it is a network hiccup, and asking again is
      // the answer to both.
      res = null
    }
    if (probe) {
      const p = res?.progress
      if (!p || !['downloading', 'verifying', 'installing', 'restarting'].includes(p.stage)) return
      installing.value = { ...p }
      followStarted = Date.now()
      updateOpen.value = true
    } else if (res) {
      const from = installing.value?.from
      if (from && res.current && res.current !== from) {
        // Back, on the new build.
        installing.value = { ...installing.value, stage: 'back' }
        notify(t('update.back').replace('{v}', res.current), 'ok')
        setTimeout(() => window.location.reload(), 1200)
        return
      }
      const p = res.progress || {}
      if (p.stage === 'failed') {
        installing.value = { ...installing.value, stage: 'failed', error: p.error }
        followStarted = 0
        return
      }
      // A panel that answers with no install under way and the old version has
      // restarted without taking the new build -- or is another process.
      if (!p.stage && installing.value?.stage === 'restarting') {
        installing.value = { ...installing.value, stage: 'failed', error: t('update.cameBackOld') }
        followStarted = 0
        return
      }
      if (p.stage) installing.value = { ...installing.value, ...p }
    } else if (installing.value && installing.value.stage !== 'downloading') {
      installing.value = { ...installing.value, stage: 'restarting' }
    }
    if (Date.now() - followStarted > COME_BACK_MS && installing.value?.stage === 'restarting') {
      installing.value = { ...installing.value, stage: 'failed', error: t('update.notBack') }
      followStarted = 0
      return
    }
    followTimer = setTimeout(() => followInstall(), 1000)
  }

  // Back on the new build, told by the version the overview reads every three
  // seconds anyway. Every panel reports that, where the progress address exists
  // only from this release on: a panel that came back on a build without it
  // would otherwise leave this page waiting.
  watch(version, (v) => {
    const p = installing.value
    if (!p || !p.from || !v || v === p.from || p.stage === 'back' || p.stage === 'failed') return
    clearTimeout(followTimer)
    installing.value = { ...p, stage: 'back' }
    notify(t('update.back').replace('{v}', v), 'ok')
    setTimeout(() => window.location.reload(), 1200)
  })

  // What to run once, as root, on a server that has no update helper yet.
  const updateCommand = 'bash <(curl -fsSL https://raw.githubusercontent.com/AbolfazlTafakori/w-ui/main/update.sh)'
  async function copyUpdateCommand() {
    try {
      await navigator.clipboard.writeText(updateCommand)
      notify(t('common.copied'), 'ok')
    } catch {
      notify(updateCommand, 'ok')
    }
  }

  // The share of the release downloaded, for the bar; null when the server did
  // not say how big it is.
  const installShare = computed(() => {
    const p = installing.value
    if (!p || !p.total) return null
    return Math.min(100, Math.round((p.received / p.total) * 100))
  })

  const installLine = computed(() => {
    const p = installing.value
    if (!p) return ''
    switch (p.stage) {
      case 'downloading':
        return p.total
          ? t('update.downloading').replace('{r}', bytes(p.received)).replace('{t}', bytes(p.total))
          : t('update.downloadingNoSize').replace('{r}', bytes(p.received))
      case 'verifying': return t('update.verifying')
      case 'installing': return t('update.installing')
      case 'restarting': return t('update.restarting').replace('{v}', p.to || '')
      case 'back': return t('update.back').replace('{v}', p.to || '')
      default: return ''
    }
  })

  function retryInstall() {
    installing.value = null
    loadUpdate(true)
  }

  onMounted(() => {
    loadUpdate()
    followInstall(true)
  })
  onBeforeUnmount(() => clearTimeout(followTimer))

  const state = {
    update, updateBusy, updateOpen, installing, openUpdate, applyUpdate, retryInstall,
    installShare, installLine, updateCommand, copyUpdateCommand,
  }
  provide(UPDATE, state)
  return state
}

// useUpdateState is the state a page started, for a component under it.
export function useUpdateState() {
  return inject(UPDATE)
}
