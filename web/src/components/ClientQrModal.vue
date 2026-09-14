<script setup>
// the classic panel's ClientQrModal: a 520px centred modal with a Collapse of one
// panel per shareable thing -- the subscription link first, then each
// device's tunnel config -- the first open, each a QrPanel.
import { onMounted, ref } from 'vue'
import { api } from '../lib/api.js'
import { t, notify } from '../lib/store.js'
import AntIcon from './AntIcon.vue'
import QrPanel from './QrPanel.vue'

const props = defineProps({ client: { type: Object, required: true } })
const emit = defineEmits(['close'])

const loading = ref(true)
const items = ref([]) // { key, tag, tagColor, meta, value, remark, downloadName, showQr }
const open = ref(new Set())

onMounted(async () => {
  try {
    const [fresh, sub] = await Promise.all([
      api.client(props.client.id),
      api.get('/api/subscription', { background: true }).catch(() => null),
    ])
    const out = []
    if (sub?.enabled) {
      try {
        const link = await api.get(`/api/clients/${props.client.id}/subscription`, { background: true })
        if (link?.link) {
          out.push({ key: 'sub', label: t('client.subscriptionTitle'), value: link.link, remark: `${fresh.name} — ${t('client.subscriptionTitle')}`, showQr: true })
        }
      } catch {
        /* no link: the panel is simply not offered */
      }
    }
    const devices = fresh.accounts || []
    for (const d of devices) {
      try {
        // One file per host on the device's inbound, as the subscription
        // hands them out; one when it has no hosts.
        const list = await api.get(`/api/devices/${d.id}/profiles`, { background: true })
        const wg = fresh.protocol === 'wireguard'
        for (const p of list) {
          const meta = [devices.length > 1 ? d.deviceName : '', p.hostName || ''].filter(Boolean).join(' · ')
          out.push({
            key: `dev-${d.id}-${p.hostId || 0}`,
            tag: wg ? t('client.wireguardConfig') : t('client.openvpnConfig'),
            tagColor: wg ? 'cyan' : 'orange',
            meta,
            value: p.body,
            remark: `${fresh.name} — ${d.deviceName}${p.hostName ? ' — ' + p.hostName : ''}`,
            downloadName: p.filename,
            // Only WireGuard imports from a camera; an OpenVPN profile carries
            // no credentials, so a code of it would be a dead end.
            showQr: wg,
          })
        }
      } catch (e) {
        notify(e.message, 'error')
      }
    }
    items.value = out
    if (out.length) open.value = new Set([out[0].key])
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    loading.value = false
  }
})

function toggle(key) {
  const next = new Set(open.value)
  next.has(key) ? next.delete(key) : next.add(key)
  open.value = next
}
</script>

<template>
  <div class="amodal-backdrop centered" @click.self="emit('close')">
    <div class="amodal" role="dialog" aria-modal="true" aria-labelledby="qr-title">
      <div class="amodal-head">
        <h2 id="qr-title" class="amodal-title">{{ t('client.qrCode') }} — {{ client.name }}</h2>
        <button class="amodal-close" :aria-label="t('common.close')" @click="emit('close')"><AntIcon name="CloseOutlined" /></button>
      </div>
      <div class="amodal-body">
        <div v-if="loading" class="aspin-nested" style="min-height: 120px">
          <div class="aspin"><span class="aspin-dot" style="margin-top: -16px"><i></i><i></i><i></i><i></i></span></div>
        </div>
        <div v-else-if="!items.length" style="padding: 24px; text-align: center; opacity: 0.6">{{ t('client.noLinks') }}</div>
        <div v-else class="acollapse">
          <div v-for="it in items" :key="it.key" class="acollapse-item" :class="{ open: open.has(it.key) }">
            <button type="button" class="acollapse-header" :aria-expanded="open.has(it.key)" @click="toggle(it.key)">
              <span class="acollapse-expand"><AntIcon name="RightOutlined" /></span>
              <span class="acollapse-label">
                <span v-if="it.tag" style="display: inline-flex; align-items: center; gap: 6px">
                  <span class="atag" :class="it.tagColor" style="margin: 0">{{ it.tag }}</span>
                  <span v-if="it.meta" style="opacity: 0.85; font-size: 12px">{{ it.meta }}</span>
                </span>
                <template v-else>{{ it.label }}</template>
              </span>
            </button>
            <div v-if="open.has(it.key)" class="acollapse-content">
              <div class="acollapse-box">
                <QrPanel :value="it.value" :remark="it.remark" :download-name="it.downloadName || ''" :show-qr="it.showQr" />
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
