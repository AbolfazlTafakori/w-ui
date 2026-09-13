<script setup>
// 3x-ui's ClientInfoModal: a 640px modal holding the info table -- one
// 13px label column, a tag per value -- then a Divider and a link row for
// the subscription, and a Divider and a ConfigBlock per tunnel config.
import { computed, onMounted, ref } from 'vue'
import { api } from '../lib/api.js'
import { store, t, notify } from '../lib/store.js'
import { bytes, dateTime, relative, isOnline } from '../lib/format.js'
import AntIcon from './AntIcon.vue'
import QrPanel from './QrPanel.vue'

const props = defineProps({
  client: { type: Object, required: true },
  interfaces: { type: Array, default: () => [] },
})
const emit = defineEmits(['close'])

const c = ref(props.client)
const sub = ref(null) // { link, token }
const configs = ref([]) // { device, body, filename }
const openCfg = ref(new Set())
const qrFor = ref(null) // { key, value, remark, x, y }
const devicesOpen = ref(false)
const loading = ref(false)

const INBOUND_CHIP_LIMIT = 1
const ifaceById = computed(() => Object.fromEntries(props.interfaces.map((i) => [i.id, i])))
const online = computed(() => (c.value.accounts || []).some((a) => isOnline(a.lastHandshake)))
const lastOnline = computed(() => {
  const ts = (c.value.accounts || []).map((a) => a.lastHandshake).filter(Boolean).sort()
  return ts.length ? ts[ts.length - 1] : null
})
const used = computed(() => Number(c.value.usedBytes || 0))
const total = computed(() => Number(c.value.quotaBytes || 0))
const remaining = computed(() => (total.value > 0 ? Math.max(0, total.value - used.value) : -1))
const inboundIds = computed(() => [...new Set((c.value.accounts || []).map((a) => a.interfaceId))])
const overflowMore = ref(false)

async function load() {
  loading.value = true
  try {
    const [fresh, subs] = await Promise.all([
      api.client(props.client.id),
      api.get('/api/subscription', { background: true }).catch(() => null),
    ])
    c.value = fresh
    if (subs?.enabled) {
      sub.value = await api.get(`/api/clients/${props.client.id}/subscription`, { background: true }).catch(() => null)
    }
    const out = []
    for (const d of fresh.accounts || []) {
      try {
        // One block per host on the device's inbound, as the subscription
        // hands them out.
        for (const p of await api.get(`/api/devices/${d.id}/profiles`, { background: true })) {
          out.push({ key: `${d.id}-${p.hostId || 0}`, device: d, hostName: p.hostName || '', body: p.body, filename: p.filename })
        }
      } catch {
        /* a device whose profile cannot be rendered is left out */
      }
    }
    configs.value = out
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    loading.value = false
  }
}
onMounted(load)

async function copy(text) {
  try {
    await navigator.clipboard.writeText(String(text))
    notify(t('common.copied'), 'success')
  } catch {
    notify(t('action.copyFailed'), 'error')
  }
}
function downloadText(text, name) {
  const url = URL.createObjectURL(new Blob([text], { type: 'text/plain;charset=utf-8' }))
  const a = document.createElement('a')
  a.href = url
  a.download = name
  a.click()
  URL.revokeObjectURL(url)
}
async function downloadSub() {
  if (!sub.value?.link) return
  try {
    const res = await fetch(sub.value.link)
    if (!res.ok) throw new Error(res.statusText)
    downloadText(await res.text(), 'subscription-standard.txt')
  } catch (e) {
    notify(e.message, 'error')
  }
}
function toggleQr(key, value, remark, e) {
  if (qrFor.value?.key === key) {
    qrFor.value = null
    return
  }
  const r = e.currentTarget.getBoundingClientRect()
  // Their Popover placement="left": beside the button, to its start.
  qrFor.value = { key, value, remark, x: r.left - 8, y: r.top + r.height / 2 }
}
function toggleCfg(key) {
  const next = new Set(openCfg.value)
  next.has(key) ? next.delete(key) : next.add(key)
  openCfg.value = next
}
const protoColor = { wireguard: 'gold', openvpn: 'orange' }
function ifaceLabel(id) {
  const i = ifaceById.value[id]
  return i ? i.name : `#${id}`
}
function ifaceColor(id) {
  return protoColor[ifaceById.value[id]?.protocol] || 'default'
}
const expiryText = computed(() => {
  if (!c.value.expiresAt) {
    if (c.value.startOnFirstUse && c.value.durationDays) return `${t('client.delayedStart')}: ${c.value.durationDays}d`
    return ''
  }
  return dateTime(c.value.expiresAt, store.locale)
})
</script>

<template>
  <div class="amodal-backdrop" @click.self="emit('close')">
    <div class="amodal w640" role="dialog" aria-modal="true" aria-labelledby="ci-title">
      <div class="amodal-head">
        <h2 id="ci-title" class="amodal-title">{{ t('client.menu.clientInfo') }} — {{ c.name }}</h2>
        <button class="amodal-close" :aria-label="t('common.close')" @click="emit('close')"><AntIcon name="CloseOutlined" /></button>
      </div>
      <div class="amodal-body">
        <table class="info-table block">
          <tbody>
            <tr>
              <td>{{ t('client.online') }}</td>
              <td>
                <span v-if="c.status === 'active' && online" class="atag green">{{ t('client.online') }}</span>
                <span v-else class="atag">{{ t('client.offline') }}</span>
                <span class="hint">{{ t('client.lastOnline') }}: {{ lastOnline ? dateTime(lastOnline, store.locale) : '-' }}</span>
              </td>
            </tr>
            <tr>
              <td>{{ t('client.statusLabel') }}</td>
              <td><span class="atag" :class="c.status === 'active' ? 'green' : ''">{{ c.status === 'disabled' ? t('status.disabled') : c.status === 'active' ? t('status.enabled') : t(`status.${c.status}`) }}</span></td>
            </tr>
            <tr>
              <td>{{ t('client.emailLabel') }}</td>
              <td><span class="atag green">{{ c.name }}</span></td>
            </tr>
            <tr>
              <td>{{ t('client.subId') }}</td>
              <td>
                <span class="atag info-large-tag ltr">{{ sub?.token || '-' }}</span>
                <button v-if="sub?.token" class="abtn text sm" :aria-label="t('action.copy')" @click="copy(sub.token)"><AntIcon name="CopyOutlined" /></button>
              </td>
            </tr>
            <tr>
              <td>{{ t('client.traffic') }}</td>
              <td>
                <span class="atag ltr">↑ {{ bytes(c.upBytes || 0, store.locale) }} / ↓ {{ bytes(c.downBytes || 0, store.locale) }}</span>
                <span class="hint ltr">{{ bytes(used, store.locale) }} / {{ total > 0 ? bytes(total, store.locale) : '∞' }}</span>
              </td>
            </tr>
            <tr>
              <td>{{ t('client.remaining') }}</td>
              <td>
                <span v-if="remaining < 0" class="atag purple">∞</span>
                <span v-else class="atag ltr" :class="remaining > 0 ? '' : 'red'">{{ bytes(remaining, store.locale) }}</span>
              </td>
            </tr>
            <tr>
              <td>{{ t('client.menu.duration') }}</td>
              <td>
                <span v-if="!c.expiresAt && !expiryText" class="atag purple">∞</span>
                <span v-else class="atag ltr" :class="!c.expiresAt ? 'blue' : ''">{{ expiryText }}</span>
                <span v-if="c.expiresAt" class="hint">{{ relative(c.expiresAt, store.locale) }}</span>
              </td>
            </tr>
            <tr>
              <td>{{ t('client.ipLimit') }}</td>
              <td><span class="atag">{{ c.deviceLimit || '∞' }}</span></td>
            </tr>
            <tr>
              <td>{{ t('client.ipLog') }}</td>
              <td><button class="abtn small" @click="devicesOpen = true"><AntIcon name="EyeOutlined" /><span v-if="(c.accounts || []).length">{{ (c.accounts || []).length }}</span></button></td>
            </tr>
            <tr>
              <td>{{ t('iface.col.createdAt') }}</td>
              <td><span class="atag ltr">{{ dateTime(c.createdAt, store.locale) }}</span></td>
            </tr>
            <tr>
              <td>{{ t('iface.col.updatedAt') }}</td>
              <td><span class="atag ltr">{{ dateTime(c.updatedAt, store.locale) }}</span></td>
            </tr>
            <tr v-if="c.group">
              <td>{{ t('client.group') }}</td>
              <td><span class="atag geekblue">{{ c.group }}</span></td>
            </tr>
            <tr v-if="c.note">
              <td>{{ t('client.comment') }}</td>
              <td><span class="atag info-large-tag">{{ c.note }}</span></td>
            </tr>
            <tr>
              <td>{{ t('client.attachedInbounds') }}</td>
              <td>
                <span v-if="!inboundIds.length" class="hint">—</span>
                <div v-else class="chips">
                  <span v-for="id in inboundIds.slice(0, INBOUND_CHIP_LIMIT)" :key="id" class="atag" :class="ifaceColor(id)" :title="ifaceLabel(id)">{{ ifaceLabel(id) }}</span>
                  <template v-if="inboundIds.length > INBOUND_CHIP_LIMIT">
                    <span class="atag default chip-more" @click="overflowMore = !overflowMore">+{{ inboundIds.length - INBOUND_CHIP_LIMIT }} {{ t('action.more') }}</span>
                    <div v-if="overflowMore" class="chips chips-stack">
                      <span v-for="id in inboundIds.slice(INBOUND_CHIP_LIMIT)" :key="id" class="atag" :class="ifaceColor(id)">{{ ifaceLabel(id) }}</span>
                    </div>
                  </template>
                </div>
              </td>
            </tr>
          </tbody>
        </table>

        <template v-if="sub?.link">
          <div class="adivider"><span class="adivider-text">{{ t('client.subscriptionTitle') }}</span></div>
          <div class="link-row">
            <span class="atag green link-row-tag">SUB</span>
            <a :href="sub.link" target="_blank" rel="noopener noreferrer" class="link-row-title link-row-title-anchor ltr" :title="sub.link">{{ sub.token }}</a>
            <div class="link-row-actions">
              <button class="abtn small icon" :title="t('action.copy')" :aria-label="t('action.copy')" @click="copy(sub.link)"><AntIcon name="CopyOutlined" /></button>
              <button class="abtn small icon" :title="t('action.download')" :aria-label="t('action.download')" @click="downloadSub"><AntIcon name="DownloadOutlined" /></button>
              <button class="abtn small icon" :title="t('client.qrCode')" :aria-label="t('client.qrCode')" @click="toggleQr('sub', sub.link, `${c.name} — ${t('client.subscriptionTitle')}`, $event)"><AntIcon name="QrcodeOutlined" /></button>
            </div>
          </div>
        </template>

        <template v-if="configs.length">
          <div class="adivider"><span class="adivider-text">{{ c.protocol === 'wireguard' ? t('client.wireguardConfig') : t('client.openvpnConfig') }}</span></div>
          <div v-for="cf in configs" :key="cf.key" class="acollapse config-block" :class="{ open: openCfg.has(cf.key) }">
            <div class="acollapse-item" :class="{ open: openCfg.has(cf.key) }">
              <div class="acollapse-header" role="button" tabindex="0" :aria-expanded="openCfg.has(cf.key)" @click="toggleCfg(cf.key)" @keydown.enter="toggleCfg(cf.key)">
                <span class="acollapse-expand"><AntIcon name="RightOutlined" /></span>
                <span class="acollapse-label"><span class="atag" :class="c.protocol === 'wireguard' ? 'cyan' : 'orange'" style="margin: 0; font-weight: 600; letter-spacing: 0.3px">{{ configs.length > 1 ? cf.device.deviceName : t('client.config') }}</span><span v-if="cf.hostName" style="margin-inline-start: 6px; font-size: 12px; opacity: 0.85">{{ cf.hostName }}</span></span>
                <div class="acollapse-extra config-block-actions" @click.stop>
                  <button class="abtn small icon" :title="t('action.copy')" :aria-label="t('action.copy')" @click="copy(cf.body)"><AntIcon name="CopyOutlined" /></button>
                  <button class="abtn small icon" :title="t('action.download')" :aria-label="t('action.download')" @click="downloadText(cf.body, cf.filename)"><AntIcon name="DownloadOutlined" /></button>
                  <button v-if="c.protocol === 'wireguard'" class="abtn small icon" :title="t('client.qrCode')" :aria-label="t('client.qrCode')" @click="toggleQr(`cfg-${cf.key}`, cf.body, `${c.name} — ${cf.device.deviceName}${cf.hostName ? ' — ' + cf.hostName : ''}`, $event)"><AntIcon name="QrcodeOutlined" /></button>
                </div>
              </div>
              <div v-if="openCfg.has(cf.key)" class="acollapse-content"><div class="acollapse-box"><code class="config-block-text">{{ cf.body }}</code></div></div>
            </div>
          </div>
        </template>
      </div>
    </div>

    <!-- Their Popover with a 220px QrPanel, to the left of the button. -->
    <Teleport to="body">
      <div v-if="qrFor" class="apopover qr-popover" :style="{ top: qrFor.y + 'px', left: qrFor.x + 'px', transform: 'translate(-100%, -50%)' }" @click.stop>
        <QrPanel :value="qrFor.value" :remark="qrFor.remark" :size="220" />
      </div>
    </Teleport>

    <!-- Their IP log: our devices, with where each was last seen from. -->
    <div v-if="devicesOpen" class="amodal-backdrop" @click.self="devicesOpen = false">
      <div class="amodal w440" role="dialog" aria-modal="true">
        <div class="amodal-head">
          <h2 class="amodal-title">{{ t('client.ipLog') }} — {{ c.name }}</h2>
          <button class="amodal-close" :aria-label="t('common.close')" @click="devicesOpen = false"><AntIcon name="CloseOutlined" /></button>
        </div>
        <div class="amodal-body">
          <div v-if="!(c.accounts || []).length" class="aempty">{{ t('common.nothingYet') }}</div>
          <div v-for="d in c.accounts || []" :key="d.id" class="ip-row">
            <span class="ip-name">{{ d.deviceName }}</span>
            <span class="atag ltr" style="margin: 0">{{ d.ip }}</span>
            <span class="atag ltr" :class="isOnline(d.lastHandshake) ? 'green' : ''" style="margin: 0">{{ d.lastEndpoint || '—' }}</span>
            <span class="hint ltr">{{ d.lastHandshake ? dateTime(d.lastHandshake, store.locale) : '-' }}</span>
          </div>
        </div>
        <div class="amodal-foot">
          <button class="abtn" :disabled="loading" @click="load"><AntIcon name="ReloadOutlined" /><span>{{ t('common.refresh') }}</span></button>
        </div>
      </div>
    </div>
  </div>
</template>

<style>
.info-table { width: 100%; border-collapse: collapse; }
.info-table.block { margin-bottom: 10px; }
.info-table td { padding: 4px 8px; vertical-align: top; }
.info-table td:first-child { width: 140px; font-size: 13px; opacity: 0.75; white-space: nowrap; }
.info-large-tag { display: inline-block; max-width: 100%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; vertical-align: middle; }
.info-table .hint { margin-left: 6px; font-size: 12px; opacity: 0.55; }
.chips { display: flex; flex-wrap: wrap; gap: 4px; align-items: center; }
.chips .atag { margin: 0; }
.chips-stack { flex-direction: column; align-items: flex-start; max-width: 280px; max-height: 280px; overflow-y: auto; }
.chip-more { cursor: pointer; user-select: none; }
.chip-more:hover { opacity: 0.85; }
.link-row { display: flex; align-items: center; gap: 8px; margin-bottom: 8px; padding: 8px 12px; border: 1px solid var(--line); border-radius: 8px; }
.link-row-tag { margin: 0; flex-shrink: 0; font-weight: 600; letter-spacing: 0.3px; }
.link-row-title { flex: 1; min-width: 0; font-size: 13px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.link-row-actions { display: flex; gap: 4px; flex-shrink: 0; }
.link-row-title-anchor { color: var(--accent); text-decoration: underline; text-decoration-color: color-mix(in srgb, var(--accent) 35%, transparent); transition: text-decoration-color 120ms ease; }
.link-row-title-anchor:hover { text-decoration-color: var(--accent); }
.config-block { margin-bottom: 8px; }
.config-block-actions { display: flex; gap: 4px; }
.config-block-text { display: block; font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; font-size: 11px; white-space: pre-wrap; word-break: break-all; direction: ltr; text-align: left; }
.qr-popover { width: 244px; }
.ip-row { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; padding: 6px 0; border-bottom: 1px solid var(--line-soft); font-size: 13px; }
.ip-row:last-child { border-bottom: 0; }
.ip-name { font-weight: 500; }
.ip-row .hint { font-size: 12px; opacity: 0.55; margin-inline-start: auto; }
</style>
