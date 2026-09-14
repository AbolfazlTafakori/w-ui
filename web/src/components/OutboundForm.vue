<script setup>
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '../lib/api.js'
import { t, notify } from '../lib/store.js'
import Icon from './Icon.vue'

// The outbound form, laid out the way the classic panel's is: a 780px dialog titled
// "+ Outbounds", two tabs -- Basics and JSON -- and a horizontal form with
// the label in the left third and the control beside it.
//
// What runs behind each protocol:
//   wireguard   -- a kernel WireGuard device the panel configures.
//   openvpn     -- an openvpn process holding a tun, from the pasted profile.
//   vless, vmess, trojan, shadowsocks, hysteria, socks, http
//               -- the Xray outbound object, verbatim, run by an xray process
//                  behind a tun. The JSON tab is that object, as in the classic panel;
//                  Basics shows the fields of it that are usually all anyone
//                  changes, and writes them back into the object.
//
// A share link, a WireGuard .conf, an OpenVPN profile or an Xray outbound
// object can be pasted or uploaded on the JSON tab and fills the form in.

const props = defineProps({
  outbound: { type: Object, default: null },
  existingTags: { type: Array, default: () => [] },
})
const emit = defineEmits(['saved', 'cancel'])

const editing = computed(() => !!props.outbound)
const busy = ref(false)
const tab = ref('basics')
const fieldError = ref({})
const formError = ref('')

const PROTOCOLS = ['wireguard', 'openvpn', 'vless', 'vmess', 'trojan', 'shadowsocks', 'hysteria', 'socks', 'http']
const XRAY = new Set(['vless', 'vmess', 'trojan', 'shadowsocks', 'hysteria', 'socks', 'http'])
const NETWORKS = [
  { value: 'tcp', label: 'RAW' },
  { value: 'kcp', label: 'mKCP' },
  { value: 'ws', label: 'WebSocket' },
  { value: 'grpc', label: 'gRPC' },
  { value: 'httpupgrade', label: 'HTTPUpgrade' },
  { value: 'xhttp', label: 'XHTTP' },
]
const FLOWS = ['', 'xtls-rprx-vision', 'xtls-rprx-vision-udp443']
const SS_METHODS = ['2022-blake3-aes-128-gcm', '2022-blake3-aes-256-gcm', '2022-blake3-chacha20-poly1305', 'aes-256-gcm', 'aes-128-gcm', 'chacha20-poly1305', 'chacha20-ietf-poly1305', 'xchacha20-poly1305', 'xchacha20-ietf-poly1305', 'none', 'plain']
const VMESS_SECURITY = ['auto', 'aes-128-gcm', 'chacha20-poly1305', 'none', 'zero']
const FINGERPRINTS = ['', 'chrome', 'firefox', 'safari', 'ios', 'android', 'edge', '360', 'qq', 'random', 'randomized']

function blank() {
  return {
    tag: '',
    kind: 'wireguard',
    note: '',
    // WireGuard
    address: '',
    privateKey: '',
    publicKey: '',
    peerPubKey: '',
    presharedKey: '',
    hopAddress: '',
    hopDns: '',
    hopMtu: 1380,
    allowedIps: ['0.0.0.0/0', '::/0'],
    keepalive: 25,
    // OpenVPN
    profile: '',
    username: '',
    password: '',
    // Xray: the object, and the fields of it Basics shows
    xray: null,
    host: '',
    port: 443,
    id: '',
    flow: '',
    encryption: 'none',
    method: '2022-blake3-aes-128-gcm',
    vmessSecurity: 'auto',
    network: 'tcp',
    path: '/',
    wsHost: '',
    serviceName: '',
    security: 'none',
    sni: '',
    fingerprint: '',
    alpn: '',
    publicKeyReality: '',
    shortId: '',
    spiderX: '',
  }
}
const form = ref(blank())

const isWireGuard = computed(() => form.value.kind === 'wireguard')
const isOpenVPN = computed(() => form.value.kind === 'openvpn')
const isXray = computed(() => XRAY.has(form.value.kind))
const isProxy = computed(() => form.value.kind === 'socks' || form.value.kind === 'http')
const hasStream = computed(() => ['vless', 'vmess', 'trojan', 'shadowsocks'].includes(form.value.kind))
const tlsAllowed = computed(() => hasStream.value && form.value.network !== 'kcp')
const realityAllowed = computed(() => form.value.kind === 'vless' && ['tcp', 'grpc', 'xhttp'].includes(form.value.network))

function splitHostPort(addr) {
  const m = /^(.*):(\d+)$/.exec(addr || '')
  return m ? { host: m[1].replace(/^\[|\]$/g, ''), port: Number(m[2]) } : { host: addr || '', port: 443 }
}

// ── Xray object <-> Basics fields ──

// Read the fields Basics shows out of an Xray outbound object.
function fieldsFromXray(ob) {
  const f = form.value
  const s = ob.settings || {}
  const vnext = (s.vnext && s.vnext[0]) || null
  const server = (s.servers && s.servers[0]) || null
  const target = vnext || server || s
  f.host = target.address || ''
  f.port = Number(target.port) || 443
  const user = vnext ? (vnext.users && vnext.users[0]) || {} : {}
  switch (ob.protocol) {
    case 'vless':
      f.id = user.id || ''
      f.flow = user.flow || ''
      f.encryption = user.encryption || 'none'
      break
    case 'vmess':
      f.id = user.id || ''
      f.vmessSecurity = user.security || 'auto'
      break
    case 'trojan':
      f.id = (server && server.password) || ''
      break
    case 'shadowsocks':
      f.id = (server && server.password) || ''
      f.method = (server && server.method) || '2022-blake3-aes-128-gcm'
      break
    case 'hysteria':
      f.id = (ob.streamSettings?.hysteriaSettings?.auth) || ''
      break
    case 'socks':
    case 'http': {
      const u = (server && server.users && server.users[0]) || {}
      f.username = u.user || ''
      f.password = u.pass || ''
      break
    }
  }
  const st = ob.streamSettings || {}
  f.network = st.network || 'tcp'
  f.security = st.security || 'none'
  const tr = st.wsSettings || st.httpupgradeSettings || st.xhttpSettings || {}
  f.path = tr.path || '/'
  f.wsHost = tr.host || ''
  f.serviceName = st.grpcSettings?.serviceName || ''
  const tls = st.tlsSettings || {}
  const re = st.realitySettings || {}
  f.sni = f.security === 'reality' ? re.serverName || '' : tls.serverName || ''
  f.fingerprint = f.security === 'reality' ? re.fingerprint || 'chrome' : tls.fingerprint || ''
  f.alpn = Array.isArray(tls.alpn) ? tls.alpn.join(',') : ''
  f.publicKeyReality = re.publicKey || ''
  f.shortId = re.shortId || ''
  f.spiderX = re.spiderX || ''
}

// Write the Basics fields back into the Xray object, keeping whatever else
// it carries that Basics does not show.
function xrayFromFields() {
  const f = form.value
  const ob = f.xray && typeof f.xray === 'object' ? JSON.parse(JSON.stringify(f.xray)) : {}
  ob.protocol = f.kind
  ob.settings = ob.settings || {}
  const s = ob.settings
  const host = f.host.trim()
  const port = Number(f.port) || 443
  switch (f.kind) {
    case 'vless': {
      s.vnext = [{ ...(s.vnext?.[0] || {}), address: host, port }]
      const u = s.vnext[0].users?.[0] || {}
      s.vnext[0].users = [{ ...u, id: f.id, flow: f.flow, encryption: f.encryption || 'none' }]
      break
    }
    case 'vmess': {
      s.vnext = [{ ...(s.vnext?.[0] || {}), address: host, port }]
      const u = s.vnext[0].users?.[0] || {}
      s.vnext[0].users = [{ ...u, id: f.id, security: f.vmessSecurity || 'auto' }]
      break
    }
    case 'trojan':
      s.servers = [{ ...(s.servers?.[0] || {}), address: host, port, password: f.id }]
      break
    case 'shadowsocks':
      s.servers = [{ ...(s.servers?.[0] || {}), address: host, port, password: f.id, method: f.method }]
      break
    case 'hysteria':
      s.address = host
      s.port = port
      s.version = 2
      break
    case 'socks':
    case 'http': {
      const srv = { ...(s.servers?.[0] || {}), address: host, port }
      if (f.username) srv.users = [{ user: f.username, pass: f.password }]
      else delete srv.users
      s.servers = [srv]
      break
    }
  }
  if (f.kind === 'hysteria') {
    const st = ob.streamSettings || {}
    st.network = 'hysteria'
    st.security = 'tls'
    st.hysteriaSettings = { ...(st.hysteriaSettings || {}), version: 2, auth: f.id, udpIdleTimeout: st.hysteriaSettings?.udpIdleTimeout ?? 60 }
    st.tlsSettings = { ...(st.tlsSettings || {}), serverName: f.sni, alpn: f.alpn ? f.alpn.split(',').map((x) => x.trim()).filter(Boolean) : ['h3'], fingerprint: f.fingerprint }
    ob.streamSettings = st
    return ob
  }
  if (hasStream.value) {
    const st = ob.streamSettings || {}
    st.network = f.network
    st.security = f.security
    for (const k of ['tcpSettings', 'kcpSettings', 'wsSettings', 'grpcSettings', 'httpupgradeSettings', 'xhttpSettings']) {
      if (k !== `${f.network}Settings`) delete st[k]
    }
    switch (f.network) {
      case 'tcp':
        st.tcpSettings = st.tcpSettings || { header: { type: 'none' } }
        break
      case 'kcp':
        st.kcpSettings = st.kcpSettings || { mtu: 1350, tti: 20, uplinkCapacity: 5, downlinkCapacity: 20, cwndMultiplier: 1, maxSendingWindow: 2097152 }
        break
      case 'ws':
        st.wsSettings = { ...(st.wsSettings || { headers: {}, heartbeatPeriod: 0 }), path: f.path || '/', host: f.wsHost }
        break
      case 'grpc':
        st.grpcSettings = { ...(st.grpcSettings || { authority: '', multiMode: false }), serviceName: f.serviceName }
        break
      case 'httpupgrade':
        st.httpupgradeSettings = { ...(st.httpupgradeSettings || { headers: {} }), path: f.path || '/', host: f.wsHost }
        break
      case 'xhttp':
        st.xhttpSettings = { ...(st.xhttpSettings || { mode: 'auto', headers: {}, xPaddingBytes: '100-1000' }), path: f.path || '/', host: f.wsHost }
        break
    }
    delete st.tlsSettings
    delete st.realitySettings
    if (f.security === 'tls') {
      st.tlsSettings = { ...(f.xray?.streamSettings?.tlsSettings || {}), serverName: f.sni, fingerprint: f.fingerprint, alpn: f.alpn ? f.alpn.split(',').map((x) => x.trim()).filter(Boolean) : [] }
    } else if (f.security === 'reality') {
      st.realitySettings = { ...(f.xray?.streamSettings?.realitySettings || {}), serverName: f.sni, fingerprint: f.fingerprint || 'chrome', publicKey: f.publicKeyReality, shortId: f.shortId, spiderX: f.spiderX }
    }
    ob.streamSettings = st
  } else if (isProxy.value) {
    delete ob.streamSettings
  }
  return ob
}

// A fresh object for a protocol chosen in the select, with the defaults
// the classic panel's form starts from.
function freshXray(kind) {
  const f = form.value
  f.host = ''
  f.port = 443
  f.id = ''
  f.flow = ''
  f.encryption = 'none'
  f.network = 'tcp'
  f.security = kind === 'trojan' || kind === 'hysteria' ? 'tls' : 'none'
  f.path = '/'
  f.wsHost = ''
  f.serviceName = ''
  f.sni = ''
  f.fingerprint = ''
  f.alpn = kind === 'hysteria' ? 'h3' : ''
  f.publicKeyReality = ''
  f.shortId = ''
  f.spiderX = ''
  f.username = ''
  f.password = ''
  f.xray = { protocol: kind, settings: {} }
}

watch(
  () => form.value.kind,
  (kind, was) => {
    if (kind === was) return
    if (XRAY.has(kind)) {
      if (!f().xray || f().xray.protocol !== kind) freshXray(kind)
    }
    if (kind === 'vless' && f().security === 'reality' && !realityAllowed.value) f().security = 'none'
  },
)
function f() {
  return form.value
}

// ── load on edit ──
onMounted(async () => {
  if (!props.outbound) return
  const o = props.outbound
  Object.assign(form.value, {
    tag: o.tag,
    kind: o.kind,
    note: o.note || '',
    address: o.address || '',
    peerPubKey: o.peerPubKey || '',
    hopAddress: o.hopAddress || '',
    hopDns: o.hopDns || '',
    hopMtu: o.hopMtu || 1380,
    allowedIps: (o.allowedIps || '').split(',').map((s) => s.trim()).filter(Boolean),
    keepalive: o.keepalive || 25,
    username: o.username || '',
  })
  if (!form.value.allowedIps.length) form.value.allowedIps = ['0.0.0.0/0', '::/0']
  if (o.kind === 'openvpn' || XRAY.has(o.kind)) {
    try {
      const { config } = await api.get(`/api/outbounds/${o.id}/config`)
      if (o.kind === 'openvpn') form.value.profile = config || ''
      else if (config) {
        const ob = JSON.parse(config)
        form.value.xray = ob
        fieldsFromXray(ob)
      }
    } catch (err) {
      notify(err.message, 'error')
    }
  }
})

watch(
  () => ({ ...form.value }),
  () => {
    fieldError.value = {}
    formError.value = ''
  },
  { deep: true },
)

const duplicateTag = computed(() => {
  const mine = form.value.tag.trim()
  if (!mine) return false
  if (editing.value && props.outbound.tag === mine) return false
  return props.existingTags.includes(mine)
})

// ── WireGuard keys ──
const derivingKey = ref(false)
async function regenerate() {
  derivingKey.value = true
  try {
    const pair = await api.post('/api/wgkey', { privateKey: '' })
    form.value.privateKey = pair.privateKey
    form.value.publicKey = pair.publicKey
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    derivingKey.value = false
  }
}
let deriveTimer = null
watch(
  () => form.value.privateKey,
  (pk) => {
    clearTimeout(deriveTimer)
    if (!pk.trim()) {
      form.value.publicKey = ''
      return
    }
    deriveTimer = setTimeout(async () => {
      try {
        const r = await api.post('/api/wgkey', { privateKey: pk.trim() })
        form.value.publicKey = r.publicKey
      } catch {
        form.value.publicKey = ''
      }
    }, 250)
  },
)
function addAllowed() {
  form.value.allowedIps.push('')
}
function removeAllowed(i) {
  form.value.allowedIps.splice(i, 1)
}

// ── payload ──
function payload() {
  const f = form.value
  const body = { tag: f.tag.trim(), kind: f.kind, note: f.note }
  if (f.kind === 'wireguard') {
    Object.assign(body, {
      address: f.address.trim(),
      privateKey: f.privateKey,
      peerPubKey: f.peerPubKey.trim(),
      presharedKey: f.presharedKey,
      hopAddress: f.hopAddress,
      hopDns: f.hopDns,
      hopMtu: Number(f.hopMtu) || 0,
      allowedIps: f.allowedIps.map((s) => s.trim()).filter(Boolean).join(', '),
      keepalive: Number(f.keepalive) || 0,
    })
  } else if (f.kind === 'openvpn') {
    Object.assign(body, { config: f.profile, username: f.username, password: f.password })
  } else {
    const ob = xrayFromFields()
    body.config = JSON.stringify(ob)
    if (isProxy.value) {
      // Proxies keep their fields in columns too, so the table can show
      // the address and the user without opening the object.
      const host = f.host.trim()
      body.address = host ? `${host.includes(':') ? `[${host}]` : host}:${Number(f.port) || 443}` : ''
      body.username = f.username
      body.password = f.password
    }
  }
  return body
}

// ── JSON tab ──
// For the Xray family the JSON is the outbound object itself, as the classic panel shows
// it. For the rest it is the object the API takes.
const jsonText = ref('')
const jsonDirty = ref(false)
const linkInput = ref('')
const fileInput = ref(null)

function toJSON() {
  const obj = isXray.value ? xrayFromFields() : payload()
  jsonText.value = JSON.stringify(obj, null, 2)
  jsonDirty.value = false
}
function applyJSON() {
  if (!jsonDirty.value) return true
  let parsed
  try {
    parsed = JSON.parse(jsonText.value)
  } catch (e) {
    notify(`JSON: ${e.message}`, 'error')
    return false
  }
  if (isXray.value && parsed.protocol) {
    if (!XRAY.has(parsed.protocol)) {
      notify(t('outbound.form.wrongLink'), 'error')
      return false
    }
    form.value.kind = parsed.protocol
    form.value.xray = parsed
    if (typeof parsed.tag === 'string' && parsed.tag) form.value.tag = parsed.tag
    fieldsFromXray(parsed)
  } else {
    fromInput(parsed)
  }
  jsonDirty.value = false
  return true
}

// Fill the form from an outbound input, as the API and the parser shape it.
function fromInput(o) {
  const f = form.value
  if (o.kind && PROTOCOLS.includes(o.kind)) f.kind = o.kind
  if (typeof o.tag === 'string' && o.tag) f.tag = o.tag
  if (typeof o.note === 'string') f.note = o.note
  if (f.kind === 'wireguard') {
    if (typeof o.address === 'string') f.address = o.address
    if (typeof o.privateKey === 'string') f.privateKey = o.privateKey
    if (typeof o.peerPubKey === 'string') f.peerPubKey = o.peerPubKey
    if (typeof o.presharedKey === 'string') f.presharedKey = o.presharedKey
    if (typeof o.hopAddress === 'string') f.hopAddress = o.hopAddress
    if (typeof o.hopDns === 'string') f.hopDns = o.hopDns
    if (o.hopMtu) f.hopMtu = Number(o.hopMtu) || 1380
    if (typeof o.allowedIps === 'string' && o.allowedIps) f.allowedIps = o.allowedIps.split(',').map((s) => s.trim()).filter(Boolean)
    if (o.keepalive) f.keepalive = Number(o.keepalive)
  } else if (f.kind === 'openvpn') {
    if (typeof o.config === 'string') f.profile = o.config
    if (typeof o.username === 'string') f.username = o.username
    if (typeof o.password === 'string') f.password = o.password
  } else if (XRAY.has(f.kind)) {
    if (typeof o.config === 'string' && o.config) {
      try {
        const ob = JSON.parse(o.config)
        f.xray = ob
        fieldsFromXray(ob)
      } catch {
        /* the parser gave it; it is JSON */
      }
    } else if (typeof o.address === 'string') {
      const hp = splitHostPort(o.address)
      f.host = hp.host
      f.port = hp.port
      if (typeof o.username === 'string') f.username = o.username
      if (typeof o.password === 'string') f.password = o.password
      f.xray = { protocol: f.kind, settings: {} }
    }
  }
}

function switchTab(next) {
  if (next === tab.value) return
  if (next === 'json') {
    toJSON()
    tab.value = 'json'
    return
  }
  if (!applyJSON()) return
  tab.value = 'basics'
}

// Anything pasted -- link, .conf, .ovpn, Xray JSON -- is read by the server,
// the same parser that runs behind the subscriptions.
async function importText(text) {
  const raw = (text || '').trim()
  if (!raw) return
  busy.value = true
  try {
    const parsed = await api.post('/api/outbounds/parse', { text: raw })
    const keep = form.value.tag
    fromInput(parsed)
    if (!form.value.tag && keep) form.value.tag = keep
    linkInput.value = ''
    jsonDirty.value = false
    notify(t('outbound.form.linkImported'), 'success')
    tab.value = 'basics'
  } catch (err) {
    notify(err.message || t('outbound.form.wrongLink'), 'error')
  } finally {
    busy.value = false
  }
}
function importLink() {
  importText(linkInput.value)
}
function pickFile() {
  fileInput.value?.click()
}
async function onFile(e) {
  const file = e.target.files?.[0]
  e.target.value = ''
  if (!file) return
  importText(await file.text())
}

async function submit() {
  if (tab.value === 'json' && !applyJSON()) return
  busy.value = true
  fieldError.value = {}
  formError.value = ''
  try {
    const body = payload()
    const saved = props.outbound
      ? await api.patch(`/api/outbounds/${props.outbound.id}`, body)
      : await api.post('/api/outbounds', body)
    notify(editing.value ? t('outbound.updated') : t('outbound.created'), 'success')
    emit('saved', saved)
  } catch (err) {
    if (err.field) {
      fieldError.value = { [err.field]: err.message }
      tab.value = 'basics'
    } else formError.value = err.message
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="modal-backdrop" @click.self="emit('cancel')">
    <div class="modal ob-modal" role="dialog" aria-modal="true" aria-labelledby="ob-title">
      <div class="card-head">
        <h2 id="ob-title">{{ editing ? `${t('action.edit')} ${t('nav.outbounds')}` : `+ ${t('nav.outbounds')}` }}</h2>
        <button class="act" :title="t('common.close')" @click="emit('cancel')">
          <Icon name="close" :size="16" />
        </button>
      </div>

      <div class="card-body">
        <div class="tabs" role="tablist">
          <button type="button" class="tab" :class="{ on: tab === 'basics' }" role="tab" :aria-selected="tab === 'basics'" @click="switchTab('basics')">
            {{ t('outbound.form.basics') }}
          </button>
          <button type="button" class="tab" :class="{ on: tab === 'json' }" role="tab" :aria-selected="tab === 'json'" @click="switchTab('json')">
            JSON
          </button>
        </div>

        <p v-if="formError" class="form-error">{{ formError }}</p>

        <form v-show="tab === 'basics'" class="hform" @submit.prevent="submit">
          <div class="hrow">
            <label for="ob-kind">{{ t('outbound.form.protocol') }}</label>
            <div class="hctl">
              <select id="ob-kind" v-model="form.kind" :disabled="outbound?.builtin">
                <option v-if="outbound?.builtin" :value="outbound.kind">{{ outbound.kind }}</option>
                <option v-for="p in PROTOCOLS" :key="p" :value="p">{{ p }}</option>
              </select>
              <p v-if="fieldError.kind" class="field-error">{{ fieldError.kind }}</p>
            </div>
          </div>

          <div class="hrow">
            <label for="ob-tag" class="req">{{ t('outbound.tag') }}</label>
            <div class="hctl">
              <input id="ob-tag" v-model="form.tag" class="ltr" :class="{ warn: duplicateTag }" :disabled="outbound?.builtin" :placeholder="t('outbound.form.tagPlaceholder')" autocomplete="off" />
              <p v-if="fieldError.tag" class="field-error">{{ fieldError.tag }}</p>
              <p v-else-if="duplicateTag" class="field-warn">{{ t('outbound.form.tagDuplicate') }}</p>
            </div>
          </div>

          <!-- Xray family: server, then the protocol's own fields -->
          <template v-if="isXray">
            <div class="hrow">
              <label for="ob-host" class="req">{{ t('outbound.address') }}</label>
              <div class="hctl">
                <input id="ob-host" v-model="form.host" class="ltr" autocomplete="off" spellcheck="false" />
                <p v-if="fieldError.address || fieldError.config" class="field-error">{{ fieldError.address || fieldError.config }}</p>
              </div>
            </div>
            <div class="hrow">
              <label for="ob-port" class="req">{{ t('outbound.form.port') }}</label>
              <div class="hctl"><input id="ob-port" v-model.number="form.port" class="ltr" type="number" min="1" max="65535" /></div>
            </div>

            <template v-if="form.kind === 'vless' || form.kind === 'vmess'">
              <div class="hrow">
                <label for="ob-id">ID</label>
                <div class="hctl"><input id="ob-id" v-model="form.id" class="ltr" placeholder="UUID" autocomplete="off" spellcheck="false" /></div>
              </div>
              <div v-if="form.kind === 'vless'" class="hrow">
                <label for="ob-enc">{{ t('outbound.form.encryption') }}</label>
                <div class="hctl"><input id="ob-enc" v-model="form.encryption" class="ltr" autocomplete="off" /></div>
              </div>
              <div v-else class="hrow">
                <label for="ob-scy">{{ t('outbound.form.security') }}</label>
                <div class="hctl">
                  <select id="ob-scy" v-model="form.vmessSecurity"><option v-for="m in VMESS_SECURITY" :key="m" :value="m">{{ m }}</option></select>
                </div>
              </div>
            </template>
            <template v-else-if="form.kind === 'trojan' || form.kind === 'hysteria'">
              <div class="hrow">
                <label for="ob-id">{{ t('outbound.password') }}</label>
                <div class="hctl"><input id="ob-id" v-model="form.id" class="ltr" autocomplete="off" spellcheck="false" /></div>
              </div>
            </template>
            <template v-else-if="form.kind === 'shadowsocks'">
              <div class="hrow">
                <label for="ob-id">{{ t('outbound.password') }}</label>
                <div class="hctl"><input id="ob-id" v-model="form.id" class="ltr" autocomplete="off" spellcheck="false" /></div>
              </div>
              <div class="hrow">
                <label for="ob-method">{{ t('outbound.form.method') }}</label>
                <div class="hctl">
                  <select id="ob-method" v-model="form.method"><option v-for="m in SS_METHODS" :key="m" :value="m">{{ m }}</option></select>
                </div>
              </div>
            </template>
            <template v-else-if="isProxy">
              <div class="hrow">
                <label for="ob-user">{{ t('outbound.username') }}</label>
                <div class="hctl"><input id="ob-user" v-model="form.username" class="ltr" autocomplete="off" /></div>
              </div>
              <div class="hrow">
                <label for="ob-pass">{{ t('outbound.password') }}</label>
                <div class="hctl"><input id="ob-pass" v-model="form.password" class="ltr" type="password" autocomplete="new-password" /></div>
              </div>
            </template>

            <!-- Transmission, then Security, the way theirs orders them -->
            <template v-if="hasStream">
              <div class="hrow">
                <label for="ob-net">{{ t('outbound.form.transmission') }}</label>
                <div class="hctl">
                  <select id="ob-net" v-model="form.network"><option v-for="n in NETWORKS" :key="n.value" :value="n.value">{{ n.label }}</option></select>
                </div>
              </div>
              <template v-if="['ws', 'httpupgrade', 'xhttp'].includes(form.network)">
                <div class="hrow">
                  <label for="ob-path">{{ t('outbound.form.path') }}</label>
                  <div class="hctl"><input id="ob-path" v-model="form.path" class="ltr" autocomplete="off" spellcheck="false" /></div>
                </div>
                <div class="hrow">
                  <label for="ob-wshost">{{ t('outbound.form.host') }}</label>
                  <div class="hctl"><input id="ob-wshost" v-model="form.wsHost" class="ltr" autocomplete="off" spellcheck="false" /></div>
                </div>
              </template>
              <div v-if="form.network === 'grpc'" class="hrow">
                <label for="ob-svc">{{ t('outbound.form.serviceName') }}</label>
                <div class="hctl"><input id="ob-svc" v-model="form.serviceName" class="ltr" autocomplete="off" spellcheck="false" /></div>
              </div>
              <div v-if="form.kind === 'vless'" class="hrow">
                <label for="ob-flow">{{ t('outbound.form.flow') }}</label>
                <div class="hctl">
                  <select id="ob-flow" v-model="form.flow"><option v-for="fl in FLOWS" :key="fl" :value="fl">{{ fl || t('outbound.form.none') }}</option></select>
                </div>
              </div>
              <div class="hrow">
                <label>{{ t('outbound.form.security') }}</label>
                <div class="hctl">
                  <div class="seg" role="group">
                    <button type="button" class="seg-btn" :class="{ on: form.security === 'none' }" @click="form.security = 'none'">{{ t('outbound.form.none') }}</button>
                    <button v-if="tlsAllowed" type="button" class="seg-btn" :class="{ on: form.security === 'tls' }" @click="form.security = 'tls'">TLS</button>
                    <button v-if="realityAllowed" type="button" class="seg-btn" :class="{ on: form.security === 'reality' }" @click="form.security = 'reality'">Reality</button>
                  </div>
                </div>
              </div>
            </template>

            <template v-if="(hasStream && form.security !== 'none') || form.kind === 'hysteria'">
              <div class="hrow">
                <label for="ob-sni">SNI</label>
                <div class="hctl"><input id="ob-sni" v-model="form.sni" class="ltr" autocomplete="off" spellcheck="false" /></div>
              </div>
              <div class="hrow">
                <label for="ob-fp">{{ t('outbound.form.fingerprint') }}</label>
                <div class="hctl">
                  <select id="ob-fp" v-model="form.fingerprint"><option v-for="fp in FINGERPRINTS" :key="fp" :value="fp">{{ fp || t('outbound.form.none') }}</option></select>
                </div>
              </div>
              <div v-if="form.security === 'tls' || form.kind === 'hysteria'" class="hrow">
                <label for="ob-alpn">ALPN</label>
                <div class="hctl"><input id="ob-alpn" v-model="form.alpn" class="ltr" placeholder="h2,http/1.1" autocomplete="off" spellcheck="false" /></div>
              </div>
              <template v-if="form.security === 'reality'">
                <div class="hrow">
                  <label for="ob-pbk" class="req">{{ t('outbound.form.publicKey') }}</label>
                  <div class="hctl"><input id="ob-pbk" v-model="form.publicKeyReality" class="ltr" autocomplete="off" spellcheck="false" /></div>
                </div>
                <div class="hrow">
                  <label for="ob-sid">Short ID</label>
                  <div class="hctl"><input id="ob-sid" v-model="form.shortId" class="ltr" autocomplete="off" spellcheck="false" /></div>
                </div>
                <div class="hrow">
                  <label for="ob-spx">SpiderX</label>
                  <div class="hctl"><input id="ob-spx" v-model="form.spiderX" class="ltr" autocomplete="off" spellcheck="false" /></div>
                </div>
              </template>
            </template>
          </template>

          <!-- OpenVPN: the profile and its login -->
          <template v-if="isOpenVPN">
            <div class="hrow">
              <label for="ob-profile" class="req">{{ t('outbound.form.profile') }}</label>
              <div class="hctl">
                <textarea id="ob-profile" v-model="form.profile" class="ltr mono" rows="10" spellcheck="false" :placeholder="'client\ndev tun\nproto udp\nremote vpn.example.com 1194\n...'"></textarea>
                <div class="under">
                  <button type="button" class="btn" @click="pickFile"><Icon name="upload" :size="14" /><span>{{ t('outbound.form.uploadOvpn') }}</span></button>
                </div>
                <p v-if="fieldError.config" class="field-error">{{ fieldError.config }}</p>
              </div>
            </div>
            <div class="hrow">
              <label for="ob-ouser">{{ t('outbound.username') }}</label>
              <div class="hctl"><input id="ob-ouser" v-model="form.username" class="ltr" autocomplete="off" /></div>
            </div>
            <div class="hrow">
              <label for="ob-opass">{{ t('outbound.password') }}</label>
              <div class="hctl"><input id="ob-opass" v-model="form.password" class="ltr" type="password" autocomplete="new-password" :placeholder="editing ? t('outbound.form.keepSecret') : ''" /></div>
            </div>
          </template>

          <!-- WireGuard: their wireguard block, field for field, with the
               .conf upload first: most people have the file, not the fields -->
          <template v-if="isWireGuard">
            <div class="hrow">
              <label>{{ t('outbound.form.configFile') }}</label>
              <div class="hctl">
                <button type="button" class="btn" @click="pickFile"><Icon name="upload" :size="14" /><span>{{ t('outbound.form.uploadConf') }}</span></button>
                <span class="hint">{{ t('outbound.form.uploadConfHint') }}</span>
              </div>
            </div>
            <div class="hrow">
              <label for="ob-hopaddr">{{ t('outbound.address') }}</label>
              <div class="hctl">
                <input id="ob-hopaddr" v-model="form.hopAddress" class="ltr" placeholder="comma-separated, e.g. 10.0.0.1,fd00::1" autocomplete="off" spellcheck="false" />
                <p v-if="fieldError.hopAddress" class="field-error">{{ fieldError.hopAddress }}</p>
              </div>
            </div>
            <div class="hrow">
              <label for="ob-priv">{{ t('outbound.form.privateKey') }}</label>
              <div class="hctl">
                <div class="compact">
                  <input id="ob-priv" v-model="form.privateKey" class="ltr" autocomplete="off" spellcheck="false" :placeholder="editing ? t('outbound.form.keepSecret') : ''" />
                  <button type="button" class="btn icon addon" :aria-label="t('outbound.form.regenerate')" :title="t('outbound.form.regenerate')" :disabled="derivingKey" @click="regenerate">
                    <Icon name="refresh" :size="14" />
                  </button>
                </div>
                <p v-if="fieldError.privateKey" class="field-error">{{ fieldError.privateKey }}</p>
              </div>
            </div>
            <div class="hrow">
              <label for="ob-pub">{{ t('outbound.form.publicKey') }}</label>
              <div class="hctl"><input id="ob-pub" :value="form.publicKey" class="ltr" disabled /></div>
            </div>
            <div class="hrow">
              <label for="ob-mtu">MTU</label>
              <div class="hctl">
                <input id="ob-mtu" v-model.number="form.hopMtu" class="ltr" type="number" min="0" />
                <p v-if="fieldError.hopMtu" class="field-error">{{ fieldError.hopMtu }}</p>
              </div>
            </div>
            <div class="hrow">
              <label>{{ t('outbound.form.peers') }}</label>
              <div class="hctl"><span class="muted small">{{ t('outbound.form.onePeer') }}</span></div>
            </div>
            <div class="hrow">
              <label></label>
              <div class="hctl item-heading">{{ t('outbound.form.peerNumber').replace('{n}', '1') }}</div>
            </div>
            <div class="hrow">
              <label for="ob-endpoint" class="req">{{ t('outbound.form.endpoint') }}</label>
              <div class="hctl">
                <input id="ob-endpoint" v-model="form.address" class="ltr" :placeholder="t('outbound.addressHint')" autocomplete="off" spellcheck="false" />
                <p v-if="fieldError.address" class="field-error">{{ fieldError.address }}</p>
              </div>
            </div>
            <div class="hrow">
              <label for="ob-peer" class="req">{{ t('outbound.form.publicKey') }}</label>
              <div class="hctl">
                <input id="ob-peer" v-model="form.peerPubKey" class="ltr" autocomplete="off" spellcheck="false" />
                <p v-if="fieldError.peerPubKey" class="field-error">{{ fieldError.peerPubKey }}</p>
              </div>
            </div>
            <div class="hrow">
              <label for="ob-psk">PSK</label>
              <div class="hctl">
                <input id="ob-psk" v-model="form.presharedKey" class="ltr" autocomplete="off" spellcheck="false" :placeholder="editing ? t('outbound.form.keepSecret') : ''" />
                <p v-if="fieldError.presharedKey" class="field-error">{{ fieldError.presharedKey }}</p>
              </div>
            </div>
            <div class="hrow">
              <label>{{ t('outbound.form.allowedIps') }}</label>
              <div class="hctl">
                <div v-for="(ip, i) in form.allowedIps" :key="i" class="compact list-row">
                  <input v-model="form.allowedIps[i]" class="ltr" :aria-label="t('outbound.form.allowedIps')" spellcheck="false" />
                  <button v-if="form.allowedIps.length > 1" type="button" class="btn icon addon" :aria-label="t('action.remove')" @click="removeAllowed(i)">
                    <Icon name="close" :size="13" />
                  </button>
                </div>
                <button type="button" class="btn sm icon" :aria-label="t('action.add')" @click="addAllowed"><Icon name="plus" :size="13" /></button>
                <p v-if="fieldError.allowedIps" class="field-error">{{ fieldError.allowedIps }}</p>
              </div>
            </div>
            <div class="hrow">
              <label for="ob-keepalive">{{ t('outbound.form.keepAlive') }}</label>
              <div class="hctl">
                <input id="ob-keepalive" v-model.number="form.keepalive" class="ltr" type="number" min="0" />
                <p v-if="fieldError.keepalive" class="field-error">{{ fieldError.keepalive }}</p>
              </div>
            </div>
          </template>

          <div class="hrow">
            <label for="ob-note">{{ t('outbound.note') }}</label>
            <div class="hctl"><input id="ob-note" v-model="form.note" autocomplete="off" /></div>
          </div>
        </form>

        <div v-show="tab === 'json'" class="json-tab">
          <div class="compact">
            <input v-model="linkInput" class="ltr" placeholder="vmess:// vless:// trojan:// ss:// hysteria2:// wireguard:// socks://" spellcheck="false" @keydown.enter.prevent="importLink" />
            <button type="button" class="btn" :title="t('outbound.form.uploadFile')" @click="pickFile"><Icon name="upload" :size="14" /></button>
            <button type="button" class="btn primary" :disabled="busy" @click="importLink">{{ t('outbound.form.import') }}</button>
          </div>
          <textarea v-model="jsonText" class="ltr mono json-editor" rows="18" spellcheck="false" @input="jsonDirty = true"></textarea>
        </div>
        <input ref="fileInput" type="file" accept=".conf,.ovpn,.json,.txt" hidden @change="onFile" />
      </div>

      <div class="modal-foot">
        <button type="button" class="btn" @click="emit('cancel')">{{ t('common.close') }}</button>
        <button type="button" class="btn primary" :disabled="busy" @click="submit">
          <span v-if="busy" class="spin"></span>
          <template v-else>{{ editing ? t('outbound.form.saveChanges') : t('outbound.form.create') }}</template>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.ob-modal {
  max-width: 780px;
}
.card-body {
  padding: 8px 24px 16px;
}
/* The tab strip is one line here, so it must not be a scroll container:
   the active tab's underline hangs 1px below the strip, and a container
   with overflow auto turns that pixel into a scrollbar with two arrows. */
.tabs {
  margin-bottom: 16px;
}
.list-row {
  margin-bottom: 4px;
}
.item-heading {
  padding: 4px 0;
  font-weight: 600;
}
.under {
  margin-top: 6px;
}
.field-error {
  margin: 4px 0 0;
  color: var(--bad);
  font-size: var(--t-sm);
}
.field-warn {
  margin: 4px 0 0;
  color: var(--warn, #faad14);
  font-size: var(--t-sm);
}
input.warn {
  border-color: var(--warn, #faad14);
}
.form-error {
  margin: 0 0 12px;
  color: var(--bad);
  font-size: var(--t-sm);
}
.json-tab {
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin-top: 10px;
}
.json-editor {
  width: 100%;
  min-height: 360px;
  max-height: 600px;
  resize: vertical;
}
@media (max-width: 640px) {
  .hrow {
    grid-template-columns: 1fr;
  }
  .hrow > label {
    text-align: start;
    padding-top: 0;
    margin-bottom: 4px;
  }
}
</style>
