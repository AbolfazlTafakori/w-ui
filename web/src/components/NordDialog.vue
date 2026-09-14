<script setup>
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '../lib/api.js'
import { t, notify } from '../lib/store.js'
import Icon from './Icon.vue'

// The NordVPN dialog, in the classic panel's order: log in with an access token or paste
// the NordLynx private key; then the two values and Log Out; Settings with
// Country, City (All Cities) and Server, each server with its load; Add
// outbound; and the servers already added, each with Reset.

const emit = defineEmits(['close', 'changed'])

const state = ref(null)
const loading = ref(true)
const busy = ref(false)
const token = ref('')
const manualKey = ref('')
const open = ref({ token: true, key: false })

const countries = ref([])
const servers = ref([])
const countryId = ref(null)
const cityId = ref(null)
const serverId = ref(null)
const loadingServers = ref(false)

async function load() {
  loading.value = true
  try {
    state.value = await api.get('/api/providers/nord')
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    loading.value = false
  }
}

async function loadCountries() {
  try {
    countries.value = await api.post('/api/providers/nord/countries')
  } catch (err) {
    notify(err.message, 'error')
  }
}
onMounted(async () => {
  await load()
  if (state.value?.loggedIn) loadCountries()
})

const cities = computed(() => countries.value.find((c) => c.id === countryId.value)?.cities || [])
const shownServers = computed(() =>
  cityId.value ? servers.value.filter((s) => s.cityId === cityId.value) : servers.value,
)
const selected = computed(() => servers.value.find((s) => s.id === serverId.value) || null)
const added = computed(() => state.value?.added || [])
const alreadyAdded = computed(
  () => !!selected.value && added.value.some((o) => o.providerRef === selected.value.hostname),
)

watch(countryId, async (id) => {
  cityId.value = null
  serverId.value = null
  servers.value = []
  if (!id) return
  loadingServers.value = true
  try {
    servers.value = await api.post('/api/providers/nord/servers', { countryId: id })
    if (!servers.value.length) notify(t('outbound.nord.noServers'), 'warning')
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    loadingServers.value = false
  }
})
watch(cityId, () => {
  if (selected.value && cityId.value && selected.value.cityId !== cityId.value) serverId.value = null
})

async function run(fn, okMsg) {
  busy.value = true
  try {
    const st = await fn()
    if (st && typeof st === 'object' && 'loggedIn' in st) state.value = st
    else await load()
    if (okMsg) notify(okMsg, 'success')
    emit('changed')
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = false
  }
}

async function login() {
  await run(() => api.post('/api/providers/nord/login', { token: token.value.trim() }))
  if (state.value?.loggedIn) loadCountries()
}
async function saveKey() {
  await run(() => api.post('/api/providers/nord/key', { privateKey: manualKey.value.trim() }))
  if (state.value?.loggedIn) loadCountries()
}
const logout = () => run(() => api.del('/api/providers/nord'))

async function addOutbound() {
  if (!selected.value) return
  if (!selected.value.publicKey) {
    notify(t('outbound.nord.noPublicKey'), 'error')
    return
  }
  const renewing = alreadyAdded.value
  await run(
    () => api.post('/api/providers/nord/outbound', { hostname: selected.value.hostname, publicKey: selected.value.publicKey }),
    renewing ? t('outbound.nord.outboundUpdated') : t('outbound.nord.outboundAdded'),
  )
}

// Reset on an added server: fetch its key again and renew the hop.
async function resetAdded(o) {
  busy.value = true
  try {
    // The server's current public key comes from the list; the country is
    // not known here, so the list is searched by hostname.
    let key = servers.value.find((s) => s.hostname === o.providerRef)?.publicKey
    if (!key) {
      key = o.peerPubKey
    }
    await api.post('/api/providers/nord/outbound', { hostname: o.providerRef, publicKey: key })
    notify(t('outbound.nord.outboundUpdated'), 'success')
    await load()
    emit('changed')
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = false
  }
}

function loadClass(n) {
  return n < 30 ? 'green' : n < 70 ? 'orange' : 'red'
}
</script>

<template>
  <div class="modal-backdrop" @click.self="emit('close')">
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="nord-title">
      <div class="card-head">
        <h2 id="nord-title">NordVPN</h2>
        <button class="act" :title="t('common.close')" @click="emit('close')">
          <Icon name="close" :size="16" />
        </button>
      </div>

      <div class="card-body">
        <div v-if="loading" class="empty"><span class="spin"></span></div>

        <template v-else-if="!state?.loggedIn">
          <div class="collapse">
            <button type="button" class="collapse-head" :aria-expanded="open.token" @click="open.token = !open.token">
              <Icon name="chevronDown" :size="14" :class="{ flip: open.token }" />
              <span>{{ t('outbound.nord.accessToken') }}</span>
            </button>
            <div v-if="open.token" class="collapse-body">
              <div class="field">
                <label for="nord-token">{{ t('outbound.nord.accessToken') }}</label>
                <input id="nord-token" v-model="token" class="ltr" :placeholder="t('outbound.nord.accessToken')" spellcheck="false" />
              </div>
              <button class="btn primary" :disabled="busy || !token.trim()" @click="login">
                <span v-if="busy" class="spin sm"></span>
                <span>{{ t('auth.login') }}</span>
              </button>
            </div>
          </div>
          <div class="collapse">
            <button type="button" class="collapse-head" :aria-expanded="open.key" @click="open.key = !open.key">
              <Icon name="chevronDown" :size="14" :class="{ flip: open.key }" />
              <span>{{ t('outbound.nord.privateKey') }}</span>
            </button>
            <div v-if="open.key" class="collapse-body">
              <div class="field">
                <label for="nord-key">{{ t('outbound.nord.privateKey') }}</label>
                <input id="nord-key" v-model="manualKey" class="ltr" :placeholder="t('outbound.nord.privateKey')" spellcheck="false" />
              </div>
              <button class="btn primary" :disabled="busy || !manualKey.trim()" @click="saveKey">
                {{ t('action.save') }}
              </button>
            </div>
          </div>
        </template>

        <template v-else>
          <table class="info">
            <tbody>
              <tr v-if="state.account.token"><td>{{ t('outbound.nord.accessToken') }}</td><td class="ltr mono">{{ state.account.token }}</td></tr>
              <tr><td>{{ t('outbound.nord.privateKey') }}</td><td class="ltr mono">{{ state.account.privateKey }}</td></tr>
            </tbody>
          </table>
          <button class="btn danger" :disabled="busy" @click="logout">
            <Icon name="logout" :size="14" />
            <span>{{ t('auth.logout') }}</span>
          </button>

          <div class="divider"><span>{{ t('outbound.warp.settings') }}</span></div>

          <div class="field">
            <label for="nord-country">{{ t('outbound.country') }}</label>
            <select id="nord-country" v-model="countryId">
              <option :value="null" disabled>{{ t('outbound.country') }}</option>
              <option v-for="c in countries" :key="c.id" :value="c.id">{{ c.name }}</option>
            </select>
          </div>
          <div class="field">
            <label for="nord-city">{{ t('outbound.city') }}</label>
            <select id="nord-city" v-model="cityId" :disabled="!countryId">
              <option :value="null">{{ t('outbound.allCities') }}</option>
              <option v-for="c in cities" :key="c.id" :value="c.id">{{ c.name }}</option>
            </select>
          </div>
          <div class="field">
            <label for="nord-server">{{ t('outbound.server') }}</label>
            <select id="nord-server" v-model="serverId" :disabled="!countryId || loadingServers">
              <option :value="null" disabled>{{ loadingServers ? '…' : t('outbound.server') }}</option>
              <option v-for="s in shownServers" :key="s.id" :value="s.id">
                {{ s.name }} · {{ s.hostname }} · {{ t('outbound.nord.serverLoad') }} {{ s.load }}%
              </option>
            </select>
            <span v-if="selected" class="hint">
              <span class="tag" :class="loadClass(selected.load)" :title="`${t('outbound.nord.serverLoad')}: ${selected.load}%`">
                {{ t('outbound.nord.serverLoad') }} {{ selected.load }}%
              </span>
              <span class="ltr mono"> {{ selected.hostname }}</span>
            </span>
          </div>

          <p v-if="alreadyAdded" class="muted small">
            {{ t('outbound.nord.alreadyAdded').replace('{reset}', t('outbound.reset')) }}
          </p>
          <button class="btn primary" :disabled="busy || !selected || alreadyAdded" @click="addOutbound">
            <span v-if="busy" class="spin sm"></span>
            <Icon v-else name="plus" :size="14" />
            <span>{{ t('outbound.warp.addOutbound') }}</span>
          </button>

          <template v-if="added.length">
            <div class="divider"><span>{{ t('outbound.nord.addedServers') }}</span></div>
            <ul class="added">
              <li v-for="o in added" :key="o.id">
                <span class="mono ltr">{{ o.tag }}</span>
                <span class="muted small ltr">{{ o.providerRef }}</span>
                <button class="btn sm" :disabled="busy" @click="resetAdded(o)">
                  <Icon name="refresh" :size="13" />
                  <span>{{ t('outbound.reset') }}</span>
                </button>
              </li>
            </ul>
          </template>
        </template>
      </div>
    </div>
  </div>
</template>

<style scoped>
.card-body {
  padding: 18px;
}
.info {
  table-layout: fixed;
  width: 100%;
  margin: 0 0 12px;
  border-collapse: collapse;
  font-size: var(--t-sm);
}
.info td {
  padding: 6px 8px;
  border-bottom: 1px solid var(--line-soft);
  overflow-wrap: anywhere;
  word-break: break-all;
  vertical-align: top;
}
.info td:first-child {
  width: 34%;
  color: var(--muted);
}
.info td.mono {
  overflow-wrap: anywhere;
}
.divider {
  display: flex;
  align-items: center;
  gap: 12px;
  margin: 18px 0 12px;
  color: var(--ink);
  font-weight: 600;
  font-size: var(--t-sm);
}
.divider::before,
.divider::after {
  content: '';
  flex: 1;
  height: 1px;
  background: var(--line);
}
.collapse {
  border: 1px solid var(--line);
  border-radius: var(--radius-sm);
  overflow: hidden;
}
.collapse + .collapse {
  margin-top: 8px;
}
.collapse-head {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 10px 12px;
  border: 0;
  background: var(--surface-2);
  color: var(--ink);
  font: inherit;
  text-align: start;
  cursor: pointer;
}
.collapse-body {
  padding: 12px;
}
.added {
  list-style: none;
  margin: 0;
  padding: 0;
}
.added li {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 0;
  border-bottom: 1px solid var(--line-soft);
}
.added li .muted {
  flex: 1;
}
</style>
