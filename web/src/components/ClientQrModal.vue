<script setup>
// The classic panel's ClientQrModal was a Collapse with one panel per file,
// which is fine for a customer with one or two and a wall for a plan of
// seventeen. Ours has two levels instead: a segmented row across the top,
// one segment per tunnel (and the subscription link first), and inside a
// tunnel the user to show -- as segments while there are few, as a select
// once there are many -- over one QrPanel. Whatever the count, the dialog
// is one screen tall and the code is always in view.
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '../lib/api.js'
import { t, notify } from '../lib/store.js'
import AntIcon from './AntIcon.vue'
import QrPanel from './QrPanel.vue'

const props = defineProps({
  client: { type: Object, required: true },
  interfaces: { type: Array, default: () => [] },
})
const emit = defineEmits(['close'])

const loading = ref(true)
// One per tunnel: { key, name, protocol, entries: [{ key, label, value, remark, downloadName, showQr, username, password }] }
const tunnels = ref([])
const subEntry = ref(null)
const tab = ref('')
const picked = ref({}) // tunnel key -> entry key

// Past four users the segments outgrow the dialog's width; a select takes
// any number in the same space.
const segmentsUpTo = 4

const current = computed(() => tunnels.value.find((x) => x.key === tab.value) || null)
const entry = computed(() => {
  if (tab.value === 'sub') return subEntry.value
  const tn = current.value
  if (!tn) return null
  return tn.entries.find((e) => e.key === picked.value[tn.key]) || tn.entries[0] || null
})

onMounted(async () => {
  try {
    const [fresh, sub, ifaces] = await Promise.all([
      api.client(props.client.id),
      api.get('/api/subscription', { background: true }).catch(() => null),
      props.interfaces.length ? Promise.resolve(props.interfaces) : api.interfaces({ background: true }).catch(() => []),
    ])
    const byId = Object.fromEntries((ifaces || []).map((i) => [i.id, i]))
    if (sub?.enabled) {
      try {
        const link = await api.get(`/api/clients/${props.client.id}/subscription`, { background: true })
        if (link?.link) {
          subEntry.value = { key: 'sub', value: link.link, remark: `${fresh.name} — ${t('client.subscriptionTitle')}`, showQr: true }
        }
      } catch {
        /* no link: the segment is simply not offered */
      }
    }
    // Files by tunnel, in the order they were issued: the first on a tunnel
    // is user 1, as the subscription page counts them.
    const accounts = [...(fresh.accounts || [])].sort((a, b) => a.id - b.id)
    const groups = new Map()
    for (const d of accounts) {
      const iface = byId[d.interfaceId] || {}
      const key = `if-${d.interfaceId}`
      if (!groups.has(key)) groups.set(key, { key, name: iface.name || `#${d.interfaceId}`, protocol: iface.protocol || fresh.protocol || 'wireguard', accounts: [] })
      groups.get(key).accounts.push(d)
    }
    const out = []
    for (const g of groups.values()) {
      const wg = g.protocol === 'wireguard'
      const several = g.accounts.length > 1
      const entries = []
      for (const [i, d] of g.accounts.entries()) {
        try {
          // One file per host on the tunnel, as the subscription hands them
          // out; one when it has no hosts.
          const list = await api.get(`/api/devices/${d.id}/profiles`, { background: true })
          const who = several ? t('client.userN', { n: i + 1 }) : fresh.name
          for (const p of list) {
            entries.push({
              key: `dev-${d.id}-${p.hostId || 0}`,
              label: p.hostName ? `${who} · ${p.hostName}` : who,
              value: p.body,
              remark: `${fresh.name} — ${d.deviceName}${p.hostName ? ' — ' + p.hostName : ''}`,
              downloadName: p.filename,
              // Only WireGuard imports from a camera; an OpenVPN profile is
              // installed from the file, with the login carried inside it.
              showQr: wg,
              username: wg ? '' : d.username || '',
              password: wg ? '' : d.password || '',
            })
          }
        } catch (e) {
          notify(e.message, 'error')
        }
      }
      out.push({ key: g.key, name: g.name, protocol: g.protocol, entries })
    }
    tunnels.value = out
    tab.value = subEntry.value ? 'sub' : out[0]?.key || ''
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    loading.value = false
  }
})

// The first user of a tunnel is shown when it is opened for the first time.
watch(tab, (k) => {
  const tn = tunnels.value.find((x) => x.key === k)
  if (tn && !picked.value[tn.key] && tn.entries[0]) picked.value = { ...picked.value, [tn.key]: tn.entries[0].key }
})

async function copy(text) {
  try {
    await navigator.clipboard.writeText(String(text))
    notify(t('common.copied'), 'success')
  } catch {
    notify(t('action.copyFailed'), 'error')
  }
}
</script>

<template>
  <div class="amodal-backdrop centered" @click.self="emit('close')">
    <div class="amodal qr-modal" role="dialog" aria-modal="true" aria-labelledby="qr-title">
      <div class="amodal-head">
        <h2 id="qr-title" class="amodal-title">{{ t('client.qrCode') }} — {{ client.name }}</h2>
        <button class="amodal-close" :aria-label="t('common.close')" @click="emit('close')"><AntIcon name="CloseOutlined" /></button>
      </div>
      <div class="amodal-body">
        <div v-if="loading" class="aspin-nested" style="min-height: 120px">
          <div class="aspin"><span class="aspin-dot" style="margin-top: -16px"><i></i><i></i><i></i><i></i></span></div>
        </div>
        <div v-else-if="!subEntry && !tunnels.length" class="aempty">{{ t('client.noLinks') }}</div>
        <template v-else>
          <!-- Level one: which thing. Hidden when there is only one. -->
          <div v-if="(subEntry ? 1 : 0) + tunnels.length > 1" class="qr-tabs" role="tablist">
            <button v-if="subEntry" type="button" class="qr-tab" role="tab" :class="{ active: tab === 'sub' }" :aria-selected="tab === 'sub'" @click="tab = 'sub'">
              <AntIcon name="LinkOutlined" /><span>{{ t('client.subscriptionTitle') }}</span>
            </button>
            <button v-for="tn in tunnels" :key="tn.key" type="button" class="qr-tab" role="tab" :class="{ active: tab === tn.key }" :aria-selected="tab === tn.key" @click="tab = tn.key">
              <i class="qr-dot" :class="tn.protocol === 'openvpn' ? 'orange' : 'cyan'"></i><span>{{ tn.name }}</span>
              <span v-if="tn.entries.length > 1" class="qr-tab-count">{{ tn.entries.length }}</span>
            </button>
          </div>

          <!-- Level two: which user, when the tunnel holds several. -->
          <div v-if="current && current.entries.length > 1" class="qr-pick">
            <div v-if="current.entries.length <= segmentsUpTo" class="aradio-group qr-seg" role="tablist">
              <button v-for="e in current.entries" :key="e.key" type="button" class="aradio-btn" :class="{ active: entry && entry.key === e.key }" @click="picked = { ...picked, [current.key]: e.key }">{{ e.label }}</button>
            </div>
            <div v-else class="aselect">
              <select :value="entry ? entry.key : ''" :aria-label="t('client.users')" @change="picked = { ...picked, [current.key]: $event.target.value }">
                <option v-for="e in current.entries" :key="e.key" :value="e.key">{{ e.label }}</option>
              </select>
            </div>
          </div>

          <div v-if="entry" class="qr-panel">
            <QrPanel :key="entry.key" :value="entry.value" :remark="entry.remark" :download-name="entry.downloadName || ''" :show-qr="entry.showQr" />
            <!-- An OpenVPN user's login, read off here for passing on. -->
            <dl v-if="entry.username" class="qr-login">
              <div>
                <dt>{{ t('client.openvpnUsername') }}</dt>
                <dd><code class="ltr">{{ entry.username }}</code><button class="abtn small icon" :title="t('action.copy')" :aria-label="t('action.copy')" @click="copy(entry.username)"><AntIcon name="CopyOutlined" /></button></dd>
              </div>
              <div>
                <dt>{{ t('client.openvpnPassword') }}</dt>
                <dd><code class="ltr">{{ entry.password }}</code><button class="abtn small icon" :title="t('action.copy')" :aria-label="t('action.copy')" @click="copy(entry.password)"><AntIcon name="CopyOutlined" /></button></dd>
              </div>
            </dl>
          </div>
        </template>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* The tunnel row: text tabs with a protocol mark, an underline on the
   chosen one, scrolling sideways on a phone rather than wrapping. */
.qr-tabs { display: flex; gap: 4px; margin: 0 0 12px; box-shadow: inset 0 -1px 0 var(--line); overflow-x: auto; overflow-y: hidden; scrollbar-width: none; }
.qr-tabs::-webkit-scrollbar { display: none; }
.qr-tab {
  display: inline-flex; align-items: center; gap: 6px; flex: 0 0 auto;
  height: 38px; padding: 0 10px;
  border: 0; border-bottom: 2px solid transparent; background: none;
  color: var(--muted); font: inherit; font-size: 14px; cursor: pointer;
  transition: color 0.2s, border-color 0.2s;
}
.qr-tab:hover { color: var(--ink); }
.qr-tab.active { color: var(--ink); border-bottom-color: var(--accent); font-weight: 500; }
.qr-tab-count { min-width: 18px; padding: 0 5px; border-radius: 9px; background: var(--surface-3); color: var(--muted); font-size: 11px; line-height: 18px; text-align: center; }
.qr-dot { width: 7px; height: 7px; border-radius: 50%; }
.qr-dot.cyan { background: var(--tag-cyan-ink, #13c2c2); }
.qr-dot.orange { background: var(--tag-orange-ink, #fa8c16); }

.qr-pick { margin-bottom: 12px; }
.qr-seg { display: flex; }
.qr-seg .aradio-btn { flex: 1 1 0; padding: 0 8px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

/* The login under an OpenVPN file: label over value, side by side. */
.qr-login { display: grid; grid-template-columns: 1fr 1fr; gap: 8px 16px; margin: 12px 0 0; }
.qr-login dt { font-size: 12px; color: var(--muted); }
.qr-login dd { display: flex; align-items: center; gap: 6px; margin: 2px 0 0; min-width: 0; }
.qr-login code { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; font-size: 13px; color: var(--ink); }
@media (max-width: 480px) { .qr-login { grid-template-columns: 1fr; } }
</style>
