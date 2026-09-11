<script setup>
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '../lib/api.js'
import { t, notify } from '../lib/store.js'
import Icon from './Icon.vue'

// The PIA dialog, in 3x-ui's order: username and password with Log In; then
// the account and Log Out; Settings with Country, Region (All regions) and
// Server; Add outbound; and the servers already added, each with Reset.

const emit = defineEmits(['close', 'changed'])

const state = ref(null)
const loading = ref(true)
const busy = ref(false)
const username = ref('')
const password = ref('')

const regions = ref([])
const country = ref(null)
const regionId = ref(null)
const hostname = ref(null)

async function load() {
  loading.value = true
  try {
    state.value = await api.get('/api/providers/pia')
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    loading.value = false
  }
}
async function loadRegions() {
  try {
    regions.value = await api.post('/api/providers/pia/regions')
  } catch (err) {
    notify(err.message, 'error')
  }
}
onMounted(async () => {
  await load()
  if (state.value?.loggedIn) loadRegions()
})

function countryName(code) {
  try {
    return new Intl.DisplayNames(undefined, { type: 'region' }).of(code) || code
  } catch {
    return code
  }
}
const countries = computed(() => {
  const set = new Map()
  for (const r of regions.value) if (!set.has(r.country)) set.set(r.country, countryName(r.country))
  return [...set.entries()].map(([code, name]) => ({ code, name })).sort((a, b) => a.name.localeCompare(b.name))
})
const countryRegions = computed(() => regions.value.filter((r) => r.country === country.value))
const servers = computed(() => {
  const pool = regionId.value ? countryRegions.value.filter((r) => r.id === regionId.value) : countryRegions.value
  return pool.flatMap((r) => r.servers.map((s) => ({ ...s, regionId: r.id, regionName: r.name })))
})
const selected = computed(() => servers.value.find((s) => s.hostname === hostname.value) || null)
const added = computed(() => state.value?.added || [])
const alreadyAdded = computed(() => !!selected.value && added.value.some((o) => o.providerRef === selected.value.hostname))

watch(country, () => {
  regionId.value = null
  hostname.value = null
  if (country.value && !countryRegions.value.length) notify(t('outbound.pia.noServers'), 'warning')
})
watch(regionId, () => {
  if (selected.value && regionId.value && selected.value.regionId !== regionId.value) hostname.value = null
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
  await run(() => api.post('/api/providers/pia/login', { username: username.value.trim(), password: password.value }))
  password.value = ''
  if (state.value?.loggedIn) loadRegions()
}
const logout = () => run(() => api.del('/api/providers/pia'))

async function addOutbound() {
  if (!selected.value) return
  const renewing = alreadyAdded.value
  await run(
    () => api.post('/api/providers/pia/outbound', { regionId: selected.value.regionId, hostname: selected.value.hostname }),
    renewing ? t('outbound.pia.outboundUpdated') : t('outbound.pia.outboundAdded'),
  )
}

async function resetAdded(o) {
  const region = regions.value.find((r) => r.servers.some((s) => s.hostname === o.providerRef))
  if (!region) {
    notify(t('outbound.pia.noServers'), 'error')
    return
  }
  await run(
    () => api.post('/api/providers/pia/outbound', { regionId: region.id, hostname: o.providerRef }),
    t('outbound.pia.outboundUpdated'),
  )
}
</script>

<template>
  <div class="modal-backdrop" @click.self="emit('close')">
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="pia-title">
      <div class="card-head">
        <h2 id="pia-title">PIA</h2>
        <button class="act" :title="t('common.close')" @click="emit('close')">
          <Icon name="close" :size="16" />
        </button>
      </div>

      <div class="card-body">
        <div v-if="loading" class="empty"><span class="spin"></span></div>

        <form v-else-if="!state?.loggedIn" @submit.prevent="login">
          <div class="field">
            <label for="pia-user">{{ t('outbound.pia.username') }}</label>
            <input id="pia-user" v-model="username" class="ltr" :placeholder="t('outbound.pia.username')" autocomplete="username" />
          </div>
          <div class="field">
            <label for="pia-pass">{{ t('outbound.pia.password') }}</label>
            <input id="pia-pass" v-model="password" class="ltr" type="password" :placeholder="t('outbound.pia.password')" autocomplete="current-password" />
          </div>
          <button type="submit" class="btn primary" :disabled="busy || !username.trim() || !password">
            <span v-if="busy" class="spin sm"></span>
            <span>{{ t('auth.login') }}</span>
          </button>
        </form>

        <template v-else>
          <table class="info">
            <tbody>
              <tr><td>{{ t('outbound.pia.account') }}</td><td class="ltr mono">{{ state.account.username }}</td></tr>
            </tbody>
          </table>
          <button class="btn danger" :disabled="busy" @click="logout">
            <Icon name="logout" :size="14" />
            <span>{{ t('auth.logout') }}</span>
          </button>

          <div class="divider"><span>{{ t('outbound.warp.settings') }}</span></div>

          <div class="field">
            <label for="pia-country">{{ t('outbound.country') }}</label>
            <select id="pia-country" v-model="country">
              <option :value="null" disabled>{{ t('outbound.country') }}</option>
              <option v-for="c in countries" :key="c.code" :value="c.code">{{ c.name }}</option>
            </select>
          </div>
          <div class="field">
            <label for="pia-region">{{ t('outbound.pia.region') }}</label>
            <select id="pia-region" v-model="regionId" :disabled="!country">
              <option :value="null">{{ t('outbound.pia.allRegions') }}</option>
              <option v-for="r in countryRegions" :key="r.id" :value="r.id">{{ r.name }}</option>
            </select>
          </div>
          <div class="field">
            <label for="pia-server">{{ t('outbound.server') }}</label>
            <select id="pia-server" v-model="hostname" :disabled="!country">
              <option :value="null" disabled>{{ t('outbound.server') }}</option>
              <option v-for="s in servers" :key="s.hostname" :value="s.hostname">{{ s.hostname }} · {{ s.regionName }}</option>
            </select>
          </div>

          <p v-if="alreadyAdded" class="muted small">
            {{ t('outbound.pia.alreadyAdded').replace('{reset}', t('outbound.reset')) }}
          </p>
          <button class="btn primary" :disabled="busy || !selected || alreadyAdded" @click="addOutbound">
            <span v-if="busy" class="spin sm"></span>
            <Icon v-else name="plus" :size="14" />
            <span>{{ t('outbound.warp.addOutbound') }}</span>
          </button>

          <template v-if="added.length">
            <div class="divider"><span>{{ t('outbound.pia.addedServers') }}</span></div>
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
}
.info td:first-child {
  width: 34%;
  color: var(--muted);
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
