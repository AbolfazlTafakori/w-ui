<script setup>
// The users a 3x-ui or Pasarguard node had of its own, brought in as
// customers here: each keeps its credentials, its links and the
// subscription its owner gave it there, and gets this panel's subscription
// beside it. A W-UI node's own tunnels and customers are made here as they
// are and handed over to this panel, a tunnel with everyone on it. Asked
// for here, never done unasked.
import { computed, onMounted, ref } from 'vue'
import { api } from '../lib/api.js'
import { store, t, notify } from '../lib/store.js'
import { bytes } from '../lib/format.js'
import AntIcon from './AntIcon.vue'

const props = defineProps({ node: { type: Object, required: true } })
const emit = defineEmits(['close', 'done'])

const L = (k, fb) => { const s = t(k); return s === k ? fb : s }
const users = ref([])
const loading = ref(true)
const error = ref('')
const busy = ref(false)
const picked = ref(new Set())
const results = ref(null)
const query = ref('')
const wui = computed(() => props.node.kind === 'remote')
// Brought in without being picked: they share a tunnel with someone who was.
const along = ref([])

async function load() {
  loading.value = true
  error.value = ''
  try {
    users.value = await api.get(`/api/nodes/${props.node.id}/users`)
    picked.value = new Set(users.value.filter((u) => !u.imported && !u.reason).map((u) => u.key))
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}
onMounted(load)

const shown = computed(() => {
  const q = query.value.trim().toLowerCase()
  return q ? users.value.filter((u) => u.name.toLowerCase().includes(q) || u.key.toLowerCase().includes(q)) : users.value
})
const open = computed(() => users.value.filter((u) => !u.imported && !u.reason))
const allPicked = computed(() => open.value.length > 0 && open.value.every((u) => picked.value.has(u.key)))

function toggle(u) {
  const s = new Set(picked.value)
  s.has(u.key) ? s.delete(u.key) : s.add(u.key)
  picked.value = s
}
function toggleAll() {
  picked.value = allPicked.value ? new Set() : new Set(open.value.map((u) => u.key))
}

function expiry(u) {
  if (u.expiresAt) return new Date(u.expiresAt).toLocaleDateString(store.locale === 'fa' ? 'fa-IR' : 'en-GB')
  if (u.durationDays) return L('nodeImport.firstUse', '{n} days from first use').replace('{n}', u.durationDays)
  return '∞'
}

async function run() {
  if (!picked.value.size) return
  busy.value = true
  try {
    const asked = new Set(picked.value)
    results.value = await api.post(`/api/nodes/${props.node.id}/users/import`, { keys: [...asked] })
    along.value = results.value.filter((r) => r.clientId && !asked.has(r.key)).map((r) => r.name)
    const ok = results.value.filter((r) => r.clientId).length
    notify(L('nodeImport.done', '{n} brought in').replace('{n}', ok), ok ? 'success' : 'error')
    emit('done')
    await load()
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="amodal-backdrop" @click.self="emit('close')">
    <div class="amodal w760" role="dialog" aria-modal="true" aria-labelledby="ni-title">
      <div class="amodal-head">
        <h2 id="ni-title" class="amodal-title">{{ wui ? L('nodeImport.wuiTitle', 'Bring in its customers') : L('nodeImport.title', 'Bring in its users') }} — {{ node.name }}</h2>
        <button class="amodal-close" :aria-label="t('action.cancel')" @click="emit('close')"><AntIcon name="CloseOutlined" /></button>
      </div>
      <div class="amodal-body">
        <div class="nf-notice"><AntIcon name="InfoCircleOutlined" /><span v-if="wui">{{ L('nodeImport.wuiExplain', 'That panel’s own tunnels and customers are made here exactly as they are. A tunnel moves with every customer on it.') }}</span><span v-else>{{ L('nodeImport.explain', 'Each user becomes a customer here and keeps its credentials, its links and the subscription it already has there; this panel’s subscription carries the same links. Its allowance, usage and expiry come with it and are kept here from then on; its own limits there are cleared.') }}</span></div>
        <div class="ni-bar">
          <label class="ainput"><input v-model="query" :placeholder="t('action.search')" /></label>
          <button type="button" class="abtn" :disabled="loading" @click="load"><AntIcon name="ReloadOutlined" /><span>{{ L('nodeImport.reload', 'Read again') }}</span></button>
        </div>
        <div v-if="loading" class="ni-empty"><span class="spin"></span></div>
        <div v-else-if="error" class="ni-empty error">{{ error }}</div>
        <div v-else-if="!users.length && wui" class="ni-empty">{{ L('nodeImport.wuiNone', 'It has no tunnels or customers of its own left.') }}</div>
        <div v-else-if="!users.length" class="ni-empty">{{ L('nodeImport.none', 'It has no users of its own. Its inbounds appear here within twenty seconds of adding it; read again then.') }}</div>
        <div v-else class="ni-list">
          <table class="atable">
            <thead>
              <tr>
                <th class="ni-check"><input type="checkbox" :checked="allPicked" :disabled="!open.length" @change="toggleAll" /></th>
                <th>{{ t('client.name') }}</th>
                <th>{{ t('client.traffic') }}</th>
                <th>{{ L('nodeImport.expiry', 'Expiry') }}</th>
                <th>{{ wui ? L('nodeImport.tunnels', 'Tunnels') : L('nodeImport.inbounds', 'Inbounds') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="u in shown" :key="u.key" :class="{ muted: u.imported || u.reason }">
                <td class="ni-check"><input type="checkbox" :checked="picked.has(u.key)" :disabled="u.imported || !!u.reason" @change="toggle(u)" /></td>
                <td>
                  <div class="ltr">{{ u.name }}</div>
                  <div v-if="u.imported" class="ni-tag ok">{{ L('nodeImport.already', 'already a customer here') }}</div>
                  <div v-else-if="u.reason" class="ni-tag">{{ u.reason }}</div>
                  <div v-else-if="!u.enabled" class="ni-tag">{{ L('nodeImport.off', 'switched off there') }}</div>
                </td>
                <td class="ltr">{{ bytes(u.usedBytes || 0, store.locale) }} / {{ u.quotaBytes ? bytes(u.quotaBytes, store.locale) : '∞' }}</td>
                <td>{{ expiry(u) }}</td>
                <td class="ltr ni-inb">{{ (u.inbounds || []).join(', ') }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <div v-if="along.length" class="ni-along">{{ L('nodeImport.along', 'Brought in with them, because they share a tunnel: {names}').replace('{names}', along.join(store.locale === 'fa' ? '، ' : ', ')) }}</div>
        <div v-if="results && results.some((r) => r.error)" class="ni-errors">
          <div v-for="r in results.filter((x) => x.error)" :key="r.key" class="ltr">{{ r.name }}: {{ r.error }}</div>
        </div>
      </div>
      <div class="amodal-foot">
        <button type="button" class="abtn" @click="emit('close')">{{ t('action.cancel') }}</button>
        <button v-if="users.length" type="button" class="abtn primary" :disabled="busy || !picked.size" @click="run">
          <span v-if="busy" class="spin sm"></span>
          <template v-else><AntIcon name="ImportOutlined" /><span>{{ L('nodeImport.run', 'Bring in {n}').replace('{n}', picked.size) }}</span></template>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.ni-bar { display: flex; gap: 8px; margin: 12px 0; }
.ni-bar .ainput { flex: 1; }
.ni-list { max-height: 50vh; overflow: auto; }
.ni-check { width: 32px; }
.ni-empty { padding: 24px; text-align: center; color: var(--muted); }
.ni-empty.error { color: var(--bad); }
.ni-tag { font-size: 12px; color: var(--muted); }
.ni-tag.ok { color: var(--ok); }
.ni-inb { font-size: 12px; max-width: 220px; }
tr.muted td { opacity: .65; }
.ni-errors { margin-top: 12px; font-size: 12px; color: var(--bad); }
.ni-along { margin-top: 12px; font-size: 12px; color: var(--muted); }
</style>
