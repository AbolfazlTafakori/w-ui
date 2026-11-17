<script setup>
// The servers a reseller or an admin sells on: each one's name, protocol and
// whether it is up, with how many of their own customers are on it and
// online. Read-only, and nothing behind the name -- the endpoint answers with
// that alone. Shown over the customer list so "my customers cannot connect"
// is answered before it becomes a ticket.
import { ref, onMounted } from 'vue'
import { api } from '../../lib/api.js'
import { useLive } from '../../lib/live.js'
import { store, t } from '../../lib/store.js'

const servers = ref(null)
const nf = (n) => Number(n || 0).toLocaleString(store.locale)

async function load(quiet = false) {
  try {
    servers.value = await api.get('/api/operator/servers', { background: quiet })
  } catch {
    // The card is a convenience over the list; a refusal (an admin without
    // the page) or a failed poll leaves it out rather than in the way.
    if (!quiet) servers.value = null
  }
}
onMounted(() => load())
// A server's state changes rarely; every ten seconds is plenty, and keeps a
// page left open from asking every three.
useLive(load, { every: 10000 })

// Up green, off grey, down amber: down is worth a look, not an alarm.
const dot = (status) => ({ up: 'dot-green', off: 'dot-gray', down: 'dot-orange' })[status] || 'dot-gray'
// Named in full, not built from the status, so every label is one the
// translations can be checked for.
const label = (status) => ({ up: t('serverStatus.up'), off: t('serverStatus.off'), down: t('serverStatus.down') })[status] || status
</script>

<template>
  <div v-if="servers && servers.length" class="acard small server-status">
    <div class="acard-body">
      <div class="server-row" v-for="s in servers" :key="s.id">
        <span class="name"><i class="dot" :class="dot(s.status)"></i>{{ s.name }}</span>
        <span class="atag">{{ s.protocol }}</span>
        <span class="state" :class="s.status">{{ label(s.status) }}</span>
        <span class="counts">
          {{ t('serverStatus.counts').replace('{clients}', nf(s.clients)).replace('{online}', nf(s.online)) }}
        </span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.server-status .acard-body { display: flex; flex-direction: column; gap: 6px; }
.server-row { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; font-size: 13px; }
.server-row .name { font-weight: 500; min-width: 120px; }
.server-row .state { color: var(--muted, var(--faint)); }
.server-row .state.down { color: var(--warn); }
.server-row .counts { margin-inline-start: auto; color: var(--muted, var(--faint)); }
.dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; margin-inline-end: 6px; vertical-align: middle; }
.dot-green { background: var(--ok); }
.dot-orange { background: var(--warn); }
.dot-gray { background: var(--faint); }
</style>
