<script setup>
// A tunnel's form, in the same dress as a customer's: Ant's labels with a
// question mark where there is more to say, rather than a paragraph under
// every field, and the settings only one protocol has gathered under that
// protocol's own heading.
import { computed, nextTick, ref, watch } from 'vue'
import XrayInboundForm from './XrayInboundForm.vue'
import { api } from '../lib/api.js'
import { store, t, notify } from '../lib/store.js'
import AntIcon from './AntIcon.vue'
import HelpTip from './HelpTip.vue'
import Toggle from './Toggle.vue'
import AwgEditor from './AwgEditor.vue'
import { fronted, proven, wsOf } from '../lib/interfaces/rows.js'

const props = defineProps({
  // When present the dialog edits that interface instead of creating one.
  iface: { type: Object, default: null },
  // The servers a tunnel can be placed on. Empty or one means there is nothing
  // to choose, and the field is not shown at all.
  nodes: { type: Array, default: () => [] },
})
const emit = defineEmits(['close', 'submit'])

const editing = computed(() => !!props.iface)

// Whether a profile uses AmneziaWG 1.5 and 2.0: ranges, or packets before
// the handshake. It decides the kind of profile a new draw keeps to.
function usesAwg2(p) {
  if (!p) return false
  return [1, 2, 3, 4].some((n) => p[`h${n}Max`] > p[`h${n}`]) || [1, 2, 3, 4, 5].some((n) => p[`i${n}`])
}

const defaults = {
  wireguard: { name: 'wg0', listenPort: 51820, subnet: '10.66.0.0/16', mtu: 1420 },
  openvpn: { name: 'ovpn0', listenPort: 1194, subnet: '10.8.0.0/16', mtu: 1500 },
  // The standard port, which the clients built into the devices use; another
  // is redirected to it.
  l2tp: { name: 'l2tp0', listenPort: 1701, subnet: '10.77.0.0/16', mtu: 1400 },
  // TCP 1723, which every built-in client dials.
  pptp: { name: 'pptp0', listenPort: 1723, subnet: '10.78.0.0/16', mtu: 1400 },
  // UDP 500 and 4500, fixed as well.
  ikev2: { name: 'ike0', listenPort: 500, subnet: '10.79.0.0/16', mtu: 1400 },
  // Any port; 443, where it looks like the web, unless something holds it.
  openconnect: { name: 'oc0', listenPort: 443, subnet: '10.81.0.0/16', mtu: 1400 },
}

// The four ways into an IKEv2 tunnel, each a type in a device's own VPN
// settings.
const IKE_MODES = ['mschapv2', 'psk', 'rsa', 'eap-tls']
const ikeModesOf = (p) => {
  if (!p) return [...IKE_MODES]
  const on = { mschapv2: p.mschapv2, psk: p.psk, rsa: p.rsa, 'eap-tls': p.eapTls }
  return IKE_MODES.filter((m) => on[m])
}

const form = ref(
  editing.value
    ? {
        name: props.iface.name,
        protocol: props.iface.protocol,
        listenPort: props.iface.listenPort,
        subnet: props.iface.subnet,
        endpointHost: props.iface.endpointHost,
        mtu: props.iface.mtu,
        dns: props.iface.dns || '',
        natInterface: props.iface.natInterface || '',
        mode: props.iface.mode,
        nodeId: props.iface.nodeId ?? 0,
        // Nested under openvpn on the way out of the API, because that is
        // where it is stored; the scrubbed view keeps it and drops the keys.
        // OpenVPN's udp or tcp; WireGuard's udp, or a stealth transport
        // beside it on a port of its own.
        transport: props.iface.protocol === 'wireguard' ? props.iface.stealth?.transport || 'udp' : props.iface.openvpn?.transport || 'udp',
        transportPort: props.iface.stealth?.port || '',
        // wui-tls's, wui-ws's, wui-poll's or wui-plain's name, path and decoy; the
        // transport's keys never leave the server.
        ws: wsOf(props.iface.stealth),
        psk: props.iface.l2tp?.psk || '',
        l2tpPlain: !!props.iface.l2tp?.allowPlain,
        ikev2Modes: ikeModesOf(props.iface.ikev2),
        oc: {
          dtls: props.iface.openconnect ? !!props.iface.openconnect.dtls : true,
          camouflage: props.iface.openconnect ? !!props.iface.openconnect.camouflage : true,
          camouflageSecret: props.iface.openconnect?.camouflageSecret || '',
          usePanelCert: props.iface.openconnect ? !!props.iface.openconnect.usePanelCert : true,
        },
        // A copy to edit; what is sent is compared with the tunnel's own.
        awg: { ...(props.iface.awg || {}) },
        awgLevel: usesAwg2(props.iface.awg) ? 'modern' : 'classic',
        enabled: props.iface.enabled,
      }
    : {
        ...defaults.wireguard,
        protocol: 'wireguard',
        endpointHost: '',
        dns: '1.1.1.1',
        natInterface: 'eth0',
        mode: 'standard',
        nodeId: 0,
        transport: 'udp',
        transportPort: '',
        ws: wsOf(null),
        psk: '',
        l2tpPlain: false,
        ikev2Modes: [...IKE_MODES],
        // OpenConnect's own: DTLS for speed, camouflage against scanners
        // (its secret drawn by the server unless one is typed), and the
        // panel's own certificate when it covers the address.
        oc: { dtls: true, camouflage: true, camouflageSecret: '', usePanelCert: true },
        // Drawn by the panel for this server the moment AmneziaWG is chosen.
        awg: null,
        awgLevel: 'modern',
        enabled: true,
      },
)
const busy = ref(false)


// The protocols in the order they are usually chosen, whatever order the
// server lists them in; one it adds later goes at the end.
const ORDER = ['wireguard', 'openvpn', 'openconnect', 'ikev2', 'l2tp', 'pptp']
const rank = (p) => (ORDER.includes(p) ? ORDER.indexOf(p) : ORDER.length)
const ownProtocols = computed(() => [...(store.meta?.protocols || ['wireguard'])].sort((a, b) => rank(a) - rank(b)))
// A 3x-ui or Pasarguard node serves what its xray does, not what this panel
// runs: the list is that server's, and its inbound is made there.
const chosenNode = computed(() => props.nodes.find((n) => n.id === form.value.nodeId) || props.nodes[0])
// A 3x-ui or Pasarguard node serves what its xray does: its inbound is made
// by XrayInboundForm, with that panel's own protocols and fields.
const xrayNode = computed(() => !editing.value && ['xui', 'pasarguard'].includes(chosenNode.value?.kind))
const isXray = xrayNode
const protocols = ownProtocols
const xin = ref(null)
// None of this panel's own tunnel sections belongs to an xray node's
// inbound; going back to a W-UI server starts it as a new tunnel again.
watch(xrayNode, (x) => {
  if (x) form.value.protocol = 'xray'
  else if (form.value.protocol === 'xray') {
    form.value.protocol = ''
    chooseProtocol('wireguard')
  }
}, { immediate: true })

// Only worth asking when there is more than one answer. A panel that has
// never added a node would otherwise show a select with a single option.
const canChooseNode = computed(() => props.nodes.length > 1)
const isWireGuard = computed(() => form.value.protocol === 'wireguard')
const isOpenVPN = computed(() => form.value.protocol === 'openvpn')
const isL2TP = computed(() => form.value.protocol === 'l2tp')
const isPPTP = computed(() => form.value.protocol === 'pptp')
const isIKEv2 = computed(() => form.value.protocol === 'ikev2')
const isOC = computed(() => form.value.protocol === 'openconnect')
// A camouflage secret drawn here, from the same alphabet the server draws
// from: letters and digits.
function drawCamouflage() {
  form.value.oc.camouflageSecret = randomPSK(16)
  clearError('openconnect.camouflageSecret')
}
// A port the clients cannot be told to change.
const fixedPort = computed(() => isL2TP.value || isPPTP.value || isIKEv2.value)
// The port a fixed-port protocol's daemon holds; another is redirected to it.
const standardPort = computed(() => (isL2TP.value ? 1701 : isPPTP.value ? 1723 : isIKEv2.value ? 500 : 0))
const ownPort = computed(() => fixedPort.value && Number(form.value.listenPort) > 0 && Number(form.value.listenPort) !== standardPort.value)

// The AmneziaWG profile is drawn by the panel, for the server the tunnel is
// on and unlike its other tunnels there: when AmneziaWG is chosen, when the
// server or the kind of profile changes, and on asking. Shown filled in, and
// sent back with the tunnel as the operator leaves it.
const drawing = ref(false)
// The MTU of the server's way out, as the draw read it; and whether the
// operator typed an MTU of their own, which the draw then sizes for rather
// than replacing.
const linkMtu = ref(0)
const mtuTouched = ref(false)
let drawSeq = 0
async function drawAwg(level = form.value.awgLevel) {
  const seq = ++drawSeq
  drawing.value = true
  try {
    // A new tunnel whose MTU nobody typed asks for the one that fits the
    // server, and takes it.
    const fit = !editing.value && !mtuTouched.value
    const q = new URLSearchParams({
      nodeId: String(form.value.nodeId || 0),
      mtu: fit ? '0' : String(Number(form.value.mtu) || 1420),
      level,
      except: String(props.iface?.id || 0),
    })
    const { linkMtu: link, mtu, ...p } = await api.get(`/api/interfaces/awg-draw?${q}`)
    if (seq !== drawSeq) return
    linkMtu.value = link || 0
    if (fit && mtu) form.value.mtu = mtu
    form.value.awgLevel = level
    form.value.awg = p
    for (const k of Object.keys(p)) clearError(`awg.${k}`)
  } catch (e) {
    if (seq === drawSeq) notify(e.message, 'error')
  } finally {
    if (seq === drawSeq) drawing.value = false
  }
}
const showsAwg = computed(() => isWireGuard.value && form.value.mode === 'amnezia')
// WireGuard's transport menu: plain UDP, or wui-udp, wui-tls, wui-ws,
// wui-poll or wui-plain beside it. The stealth transports read standard WireGuard only;
// AmneziaWG's packets are its own.
const wgTransports = computed(() => isWireGuard.value && form.value.mode !== 'amnezia')
const showsTransport = computed(() => !xrayNode.value && (isOpenVPN.value || wgTransports.value))
const stealthChosen = computed(() => wgTransports.value && form.value.transport !== 'udp')
// wui-tls, wui-ws, wui-poll and wui-plain share a name, a path and a decoy.
// wui-ws, wui-poll and wui-plain are behind the operator's front and have
// its fields too; wui-poll is wui-ws's settings for a CDN without WebSocket;
// wui-plain has no TLS, so its port is 80, its decoy is required, and its
// token travels in a cookie of its own. wui-tls is the server itself, on its
// own port, with a decoy that must be an http:// address and a certificate.
const wsChosen = computed(() => stealthChosen.value && proven(form.value.transport))
const frontChosen = computed(() => stealthChosen.value && fronted(form.value.transport))
const plainChosen = computed(() => stealthChosen.value && form.value.transport === 'wui-plain')
const tlsChosen = computed(() => stealthChosen.value && form.value.transport === 'wui-tls')
const pollChosen = computed(() => stealthChosen.value && form.value.transport === 'wui-poll')
const shapeChosen = computed(() => stealthChosen.value && form.value.transport === 'wui-shape')
// wui-faketls is the server on its own port that borrows a real site: no
// front, no path, no decoy -- only the borrowed site's host:port.
const fakeChosen = computed(() => stealthChosen.value && form.value.transport === 'wui-faketls')
// The advanced card gathers routing and MTU, and a WireGuard or OpenVPN
// tunnel's mode and transport. Xray has none of these; everything else does.
const showsAdvanced = computed(() => !isXray.value)
// The line under the protocol select, a word on what it is.
const protoHint = computed(() => t(isWireGuard.value ? 'interface.protoHintWg' : 'interface.protoHintGeneric'))
// Whose certificate a saved wui-tls tunnel serves, when it is still the one
// shown.
const tlsCertSource = computed(() => (editing.value && tlsChosen.value && props.iface.stealth?.transport === 'wui-tls' ? props.iface.stealth?.certSource || '' : ''))
function chooseTransport() {
  clearError('transport')
  clearError('transportPort')
  // A port to start from, beside the tunnel's own; the operator may move it.
  if (stealthChosen.value && !form.value.transportPort) form.value.transportPort = (Number(form.value.listenPort) || 51820) + 1
  // A path and a cookie are a transport's own: one switched to starts
  // without and draws its own, and the tunnel's come back with its own
  // transport. Nothing wui-plain showed in the clear goes on to wui-ws.
  if (wsChosen.value) {
    const saved = props.iface?.stealth
    const own = saved?.transport === form.value.transport ? wsOf(saved) : null
    form.value.ws.path = own?.path || ''
    form.value.ws.cookie = own?.cookie || ''
    form.value.ws.shape = own?.shape || 'get-poll'
  }
}
const transportHint = computed(() => {
  if (isWireGuard.value) {
    if (form.value.transport === 'wui-tls') return t('interface.transportWuiTlsHint')
    if (form.value.transport === 'wui-ws') return t('interface.transportWuiWsHint')
    if (form.value.transport === 'wui-poll') return t('interface.transportWuiPollHint')
    if (form.value.transport === 'wui-shape') return t('interface.transportWuiShapeHint')
    if (form.value.transport === 'wui-plain') return t('interface.transportWuiPlainHint')
    if (form.value.transport === 'wui-faketls') return t('interface.transportWuiFakeTLSHint')
    return form.value.transport === 'udp' ? t('interface.transportWgUdpHint') : t('interface.transportWuiUdpHint')
  }
  return form.value.transport === 'tcp' ? t('interface.transportTcpHint') : t('interface.transportUdpHint')
})
watch(
  () => [showsAwg.value, form.value.nodeId],
  ([on]) => {
    if (on && !editing.value) drawAwg()
  },
)
watch(
  () => form.value.mode,
  (m) => {
    // An existing tunnel switched to AmneziaWG has no profile yet.
    if (editing.value && m === 'amnezia' && props.iface.mode !== 'amnezia') drawAwg()
  },
)
// A larger MTU leaves less room for the data packets' padding: the drawn
// profile is fitted again, unless it is the operator's own by now.
watch(
  () => form.value.mtu,
  (m) => {
    if (!showsAwg.value || !form.value.awg || editing.value) return
    // The link less IPv4, UDP and WireGuard's own header, less the MTU.
    const room = Math.max(0, (linkMtu.value || 1500) - 60 - Number(m))
    if ((form.value.awg.s4 || 0) > room) drawAwg()
  },
)

// A tunnel lets devices in at least one way: the last one on cannot be
// switched off.
function toggleIkeMode(m, on) {
  const now = new Set(form.value.ikev2Modes)
  if (on) now.add(m)
  else if (now.size > 1) now.delete(m)
  form.value.ikev2Modes = IKE_MODES.filter((x) => now.has(x))
  clearError('ikev2Modes')
}

// The devices an L2TP tunnel serves, read once when the form first shows
// one: with IPsec always, and those without when the switch is on.
const l2tpDevices = ref([])
watch(
  isL2TP,
  async (on) => {
    if (!on || l2tpDevices.value.length) return
    try {
      l2tpDevices.value = await api.l2tpDevices()
    } catch {
      /* the list is a convenience; the form works without it */
    }
  },
  { immediate: true },
)
const deviceName = (d) => (store.locale === 'fa' && d.name.fa) || d.name.en
const ipsecDevices = computed(() => l2tpDevices.value.filter((d) => d.supports.includes('l2tp')))
const plainDevices = computed(() => l2tpDevices.value.filter((d) => d.supports.includes('l2tp-plain') && !d.supports.includes('l2tp')))
const devicesOpen = ref(false)

// Below this a key is said to be weak: kept for devices that already hold
// it, never the panel's own choice. From this on it is as strong as the one
// the server draws.
const WEAK_PSK = 8
const STRONG_PSK = 16
const pskStrength = computed(() => {
  const n = form.value.psk.length
  if (!n) return null
  if (n < WEAK_PSK) return { cls: 'orange', key: 'interface.pskWeakTag' }
  if (n < STRONG_PSK) return { cls: 'gold', key: 'interface.pskFairTag' }
  return { cls: 'green', key: 'interface.pskStrongTag' }
})

// A key from the same alphabet the server draws one from when the field is
// left empty: letters and digits, which every device's VPN settings take.
function randomPSK(n = 24) {
  const alphabet = 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789'
  // Bytes past the last whole multiple of the alphabet are drawn again, so
  // every character is as likely as every other.
  const limit = 256 - (256 % alphabet.length)
  let out = ''
  while (out.length < n) {
    const bytes = new Uint8Array(n)
    crypto.getRandomValues(bytes)
    for (const b of bytes) if (b < limit && out.length < n) out += alphabet[b % alphabet.length]
  }
  return out
}

async function copy(v) {
  try {
    await navigator.clipboard.writeText(v)
    notify(t('action.copied'), 'success')
  } catch {
    notify(t('action.copyFailed'), 'error')
  }
}

// Switching protocol swaps in that protocol's conventional port and subnet, so
// the operator is not left with a WireGuard port on an OpenVPN interface.
function chooseProtocol(p) {
  if (editing.value || form.value.protocol === p) return
  form.value.protocol = p
  clearError('protocol')
  Object.assign(form.value, defaults[p])
  if (p !== 'wireguard') form.value.mode = 'standard'
  // Each protocol's transports are its own.
  form.value.transport = 'udp'
  form.value.transportPort = ''
  form.value.ws = wsOf(null)
}

const fieldError = ref('')
const fieldName = ref('')

function clearError(name) {
  if (fieldName.value === name) {
    fieldName.value = ''
    fieldError.value = ''
  }
}

async function submit() {
  busy.value = true
  fieldName.value = ''
  fieldError.value = ''
  try {
    if (xrayNode.value) {
      let xray
      try {
        xray = xin.value.payload()
      } catch (e) {
        fieldError.value = e.message
        return
      }
      await emit('submit', { name: form.value.name, nodeId: form.value.nodeId, listenPort: form.value.listenPort, xray })
      return
    }
    await emit('submit', { ...form.value })
  } finally {
    busy.value = false
  }
}

// Called by the page when the server refused the submission.
function showError(err) {
  if (err?.field) {
    fieldName.value = err.field
    fieldError.value = err.message
    // Put the operator in the field being complained about, so the fix is one
    // keystroke away rather than a hunt down the form.
    nextTick(() => {
      const el = document.querySelector(`[data-field="${err.field}"]`)
      el?.focus()
      el?.scrollIntoView({ block: 'center', behavior: 'smooth' })
    })
  }
}

defineExpose({ showError })
</script>

<template>
  <div class="amodal-backdrop" @click.self="emit('close')">
    <div class="amodal w720" role="dialog" aria-modal="true" aria-labelledby="if-title">
      <div class="amodal-head if-head">
        <div class="if-head-main">
          <span class="if-head-badge" aria-hidden="true"><AntIcon name="DeploymentUnitOutlined" /></span>
          <div class="if-head-text">
            <h2 id="if-title" class="amodal-title">{{ editing ? t('interface.edit') : t('interface.create') }}</h2>
            <p class="if-head-sub">{{ t('interface.formSubtitle') }}</p>
          </div>
        </div>
        <button class="amodal-close" :aria-label="t('action.cancel')" @click="emit('close')"><AntIcon name="CloseOutlined" /></button>
      </div>

      <div class="amodal-body if-body">
        <form id="iface-form" class="if-form" @submit.prevent="submit">
          <div v-if="fieldError && !fieldName" class="aalert error" role="alert"><AntIcon name="CloseCircleFilled" /><span>{{ fieldError }}</span></div>

          <div v-if="editing" class="aform-item enabled-line">
            <Toggle v-model="form.enabled" :label="t('table.enabled')" />
            <span class="enabled-text">{{ t('table.enabled') }}</span>
            <HelpTip :text="t('interface.enabledHint')" />
          </div>

          <div class="section-head"><span>{{ t('interface.sectionBase') }}</span><span class="section-rule"></span></div>

          <div class="arow16">
            <!-- The server that terminates this tunnel. Fixed after creation,
                 because moving a live tunnel between machines would strand
                 every customer on it. -->
            <div v-if="!xrayNode" class="acol12">
              <div class="aform-item">
                <label class="aform-label" for="if-node">{{ t('interface.node') }} <HelpTip v-if="editing" :text="t('interface.nodeLocked')" /></label>
                <div class="aselect has-ic" :class="{ disabled: editing || !canChooseNode, invalid: fieldName === 'nodeId' }">
                  <span class="in-ic"><AntIcon name="CloudServerOutlined" /></span>
                  <select id="if-node" v-model="form.nodeId" data-field="nodeId" :disabled="editing || !canChooseNode" @change="clearError('nodeId')">
                    <option v-for="n in nodes" :key="n.id" :value="n.id">{{ n.name }}{{ n.kind === 'local' ? ` — ${t('interface.thisServer')}` : '' }}</option>
                  </select>
                </div>
                <p v-if="fieldName === 'nodeId'" class="field-error" role="alert">{{ fieldError }}</p>
                <p v-else class="field-hint">{{ t('interface.serverHint') }}</p>
              </div>
            </div>
            <div v-if="!xrayNode" class="acol12">
              <div class="aform-item">
                <label class="aform-label required proto-label" for="if-proto">
                  <span>{{ t('client.protocol') }}</span>
                  <span v-if="isWireGuard" class="proto-badge">{{ t('interface.protoBadge') }}</span>
                </label>
                <div class="aselect has-ic" :class="{ disabled: editing, invalid: fieldName === 'protocol' }">
                  <span class="in-ic ok"><AntIcon name="SafetyCertificateOutlined" /></span>
                  <select id="if-proto" :value="form.protocol" data-field="protocol" :disabled="editing" @change="chooseProtocol($event.target.value)">
                    <option v-for="p in protocols" :key="p" :value="p">{{ t(`protocol.${p}`) }}</option>
                  </select>
                </div>
                <p v-if="fieldName === 'protocol'" class="field-error" role="alert">{{ fieldError }}</p>
                <p v-else class="field-hint">{{ protoHint }}</p>
              </div>
            </div>

          </div>

          <div class="arow16">
            <div v-if="!isXray" class="acol12">
              <div class="aform-item">
                <label class="aform-label required" for="if-name">{{ t('interface.name') }}</label>
                <label class="ainput block has-ic" :class="{ invalid: fieldName === 'name' }">
                  <span class="in-ic mono-ic">#</span>
                  <input id="if-name" v-model="form.name" data-field="name" class="ltr" required @input="clearError('name')" />
                </label>
                <p v-if="fieldName === 'name'" class="field-error" role="alert">{{ fieldError }}</p>
                <p v-else class="field-hint">{{ t('interface.nameHint') }}</p>
              </div>
            </div>
            <div v-if="!isXray" class="acol12">
              <div class="aform-item">
                <label class="aform-label required" for="if-host">{{ t('interface.endpoint') }} <HelpTip :text="t('interface.endpointHint')" /></label>
                <label class="ainput block has-ic" :class="{ invalid: fieldName === 'endpointHost' }">
                  <span class="in-ic"><AntIcon name="LinkOutlined" /></span>
                  <input id="if-host" v-model="form.endpointHost" data-field="endpointHost" class="ltr" placeholder="vpn.example.com" required @input="clearError('endpointHost')" />
                </label>
                <p v-if="fieldName === 'endpointHost'" class="field-error" role="alert">{{ fieldError }}</p>
                <p v-else class="field-hint">{{ t('interface.endpointSub') }}</p>
              </div>
            </div>
          </div>

          <div v-if="!isXray" class="section-head"><span>{{ t('interface.sectionNetwork') }}</span><span class="section-rule"></span></div>

          <div v-if="!isXray" class="arow16">
            <div class="acol12">
              <div class="aform-item">
                <label class="aform-label required" for="if-subnet">{{ t('interface.subnet') }} <HelpTip :text="t('interface.subnetHint')" /></label>
                <label class="ainput block has-ic" :class="{ invalid: fieldName === 'subnet' }">
                  <span class="in-ic"><AntIcon name="DeploymentUnitOutlined" /></span>
                  <input id="if-subnet" v-model="form.subnet" data-field="subnet" class="ltr" required @input="clearError('subnet')" />
                </label>
                <p v-if="fieldName === 'subnet'" class="field-error" role="alert">{{ fieldError }}</p>
                <p v-else class="field-hint">{{ t('interface.subnetSub') }}</p>
              </div>
            </div>
            <div class="acol6">
              <div class="aform-item">
                <label class="aform-label required" for="if-port">{{ t('interface.port') }} <HelpTip v-if="fixedPort" :text="t(isPPTP ? 'interface.pptpPortHint' : isIKEv2 ? 'interface.ikev2PortHint' : 'interface.l2tpPortHint')" /></label>
                <label class="ainput number" :class="{ invalid: fieldName === 'listenPort' }">
                  <input id="if-port" v-model="form.listenPort" data-field="listenPort" type="number" min="1" max="65535" class="ltr" required @input="clearError('listenPort')" />
                </label>
                <p v-if="fieldName === 'listenPort'" class="field-error" role="alert">{{ fieldError }}</p>
                <p v-else-if="ownPort" class="aalert warning slim port-own"><AntIcon name="WarningOutlined" /><span>{{ t('interface.portNotStandard', { port: form.listenPort, std: standardPort }) }}</span></p>
                <p v-else class="field-hint">{{ t('interface.portSub') }}</p>
              </div>
            </div>
            <div class="acol6">
              <div class="aform-item">
                <label class="aform-label" for="if-nat">{{ t('interface.natInterface') }}</label>
                <label class="ainput block"><input id="if-nat" v-model="form.natInterface" class="ltr" placeholder="eth0" /></label>
                <p class="field-hint">{{ t('interface.egressSub') }}</p>
              </div>
            </div>
          </div>

          <XrayInboundForm v-if="xrayNode" ref="xin" :node="chosenNode" :port="form.listenPort"
            @port="(v) => (form.listenPort = v)" @name="(v) => (form.name = v)" @error="(m) => (fieldError = m)" />

          <div v-if="showsAdvanced" class="adv-box">
            <div class="adv-head">
              <span class="adv-head-main">
                <svg class="adv-ic" viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
                  <line x1="4" y1="7" x2="20" y2="7" /><circle cx="9" cy="7" r="2.3" fill="var(--surface-2)" />
                  <line x1="4" y1="12" x2="20" y2="12" /><circle cx="15" cy="12" r="2.3" fill="var(--surface-2)" />
                  <line x1="4" y1="17" x2="20" y2="17" /><circle cx="8" cy="17" r="2.3" fill="var(--surface-2)" />
                </svg>
                {{ t('interface.sectionAdvanced') }}
              </span>
              <span class="adv-head-tag">{{ t('interface.sectionAdvancedTag') }}</span>
            </div>

          <!-- Mode and transport side by side, as the comp has them. Mode is
               WireGuard's alone; transport is WireGuard's and OpenVPN's. -->
          <div v-if="(!xrayNode && isWireGuard) || showsTransport" class="arow16">
            <div v-if="!xrayNode && isWireGuard" class="acol12">
              <div class="aform-item">
                <label class="aform-label" for="if-mode">{{ t('interface.mode') }} <HelpTip :text="t('interface.modeHint')" /></label>
                <div class="aselect" :class="{ invalid: fieldName === 'mode' }">
                  <select id="if-mode" v-model="form.mode" data-field="mode" @change="clearError('mode')">
                    <option value="standard">{{ t('interface.mode.standard') }}</option>
                    <option value="amnezia">{{ t('interface.mode.amnezia') }}</option>
                  </select>
                </div>
                <p v-if="fieldName === 'mode'" class="field-error" role="alert">{{ fieldError }}</p>
              </div>
            </div>
            <div v-if="showsTransport" class="acol12">
              <div class="aform-item">
                <label class="aform-label" for="if-transport">{{ t('interface.transport') }} <HelpTip :text="transportHint" /></label>
                <div class="aselect" :class="{ invalid: fieldName === 'transport' }">
                  <select id="if-transport" v-model="form.transport" data-field="transport" @change="chooseTransport">
                    <template v-if="isWireGuard">
                      <option value="udp">{{ t('interface.transportWgUdp') }}</option>
                      <option value="wui-udp">{{ t('interface.transportWuiUdp') }}</option>
                      <option value="wui-tls">{{ t('interface.transportWuiTls') }}</option>
                      <option value="wui-ws">{{ t('interface.transportWuiWs') }}</option>
                      <option value="wui-poll">{{ t('interface.transportWuiPoll') }}</option>
                      <option value="wui-shape">{{ t('interface.transportWuiShape') }}</option>
                      <option value="wui-plain">{{ t('interface.transportWuiPlain') }}</option>
                      <option value="wui-faketls">{{ t('interface.transportWuiFakeTLS') }}</option>
                    </template>
                    <template v-else>
                      <option value="udp">{{ t('interface.transportUdp') }}</option>
                      <option value="tcp">{{ t('interface.transportTcp') }}</option>
                    </template>
                  </select>
                </div>
                <p v-if="fieldName === 'transport'" class="field-error" role="alert">{{ fieldError }}</p>
              </div>
            </div>
          </div>

          <!-- The stealth transport's own port, beside the tunnel's. -->
          <div v-if="stealthChosen" class="arow16">
            <div class="acol12">
              <div class="aform-item">
                <label class="aform-label" for="if-transport-port">
                  {{ tlsChosen ? t('interface.transportPortTls') : plainChosen ? t('interface.transportPortPlain') : pollChosen ? t('interface.transportPortPoll') : shapeChosen ? t('interface.transportPortShape') : fakeChosen ? t('interface.transportPortFakeTLS') : wsChosen ? t('interface.transportPortWs') : t('interface.transportPort') }}
                  <HelpTip :text="tlsChosen ? t('interface.transportPortTlsHint') : plainChosen ? t('interface.transportPortPlainHint') : pollChosen ? t('interface.transportPortPollHint') : shapeChosen ? t('interface.transportPortShapeHint') : fakeChosen ? t('interface.transportPortFakeTLSHint') : wsChosen ? t('interface.transportPortWsHint') : t('interface.transportPortHint')" />
                </label>
                <label class="ainput number" :class="{ invalid: fieldName === 'transportPort' }">
                  <input id="if-transport-port" v-model="form.transportPort" data-field="transportPort" type="number" min="1" max="65535" class="ltr" @input="clearError('transportPort')" />
                </label>
                <p v-if="fieldName === 'transportPort'" class="field-error" role="alert">{{ fieldError }}</p>
              </div>
            </div>
          </div>

          <!-- An AmneziaWG tunnel's profile, every value editable. A tunnel
               being made gets one drawn for it; it is edited once it exists. -->
          <AwgEditor
            v-if="showsAwg && form.awg"
            v-model="form.awg"
            v-model:level="form.awgLevel"
            :drawing="drawing"
            :editing="editing && props.iface.mode === 'amnezia'"
            :field-name="fieldName"
            :field-error="fieldError"
            @draw="(lv) => drawAwg(lv || form.awgLevel)"
            @clear="clearError"
          />
          <div v-else-if="showsAwg && drawing" class="awg-wait"><AntIcon name="LoadingOutlined" class="spin" />{{ t('interface.awgDrawing') }}</div>

          <div v-if="!isXray" class="arow16">
            <div class="acol12">
              <div class="aform-item">
                <label class="aform-label" for="if-dns">DNS</label>
                <label class="ainput block has-ic">
                  <span class="in-ic"><AntIcon name="GlobalOutlined" /></span>
                  <input id="if-dns" v-model="form.dns" class="ltr" placeholder="1.1.1.1" />
                </label>
                <p class="field-hint">{{ t('interface.dnsSub') }}</p>
              </div>
            </div>
            <div class="acol12">
              <div class="aform-item">
                <label class="aform-label" for="if-mtu">MTU</label>
                <label class="ainput number" :class="{ invalid: fieldName === 'mtu' }">
                  <input id="if-mtu" v-model="form.mtu" data-field="mtu" type="number" min="576" max="9000" class="ltr" @input="clearError('mtu'); mtuTouched = true" />
                </label>
                <p v-if="fieldName === 'mtu'" class="field-error" role="alert">{{ fieldError }}</p>
                <p v-else-if="showsAwg && linkMtu" class="aform-extra">{{ t('interface.awgLinkMtu').replace('{n}', linkMtu) }}</p>
                <p v-else class="field-hint">{{ t('interface.mtuSub') }}</p>
              </div>
            </div>
          </div>

          <!-- wui-faketls: the borrowed site every stranger is relayed to,
               and whose name a device's hello asks for. -->
          <template v-if="fakeChosen">
            <div class="arow16">
              <div class="acol24">
                <div class="aform-item">
                  <label class="aform-label required" for="if-fake-site">{{ t('interface.fakeSite') }} <HelpTip :text="t('interface.fakeSiteHint')" /></label>
                  <label class="ainput block" :class="{ invalid: fieldName === 'transportWs.site' }">
                    <input id="if-fake-site" v-model.trim="form.ws.site" data-field="transportWs.site" class="ltr" placeholder="www.wikipedia.org:443" required @input="clearError('transportWs.site')" />
                  </label>
                  <p v-if="fieldName === 'transportWs.site'" class="field-error" role="alert">{{ fieldError }}</p>
                </div>
              </div>
            </div>
          </template>

          <!-- wui-tls, wui-ws, wui-poll and wui-plain: the name devices
               reach, the path they send on, and the site every stranger sees.
               The path is drawn when left empty; the operator's web server
               needs it, so it shows once saved. wui-ws, wui-poll and
               wui-plain add their front's
               settings, wui-plain its cookie's name, and wui-tls whose
               certificate it serves. -->
          <template v-if="wsChosen">
            <div class="arow16">
              <div class="acol12">
                <div class="aform-item">
                  <label class="aform-label" for="if-ws-host">{{ tlsChosen ? t('interface.tlsHost') : t('interface.wsHost') }} <HelpTip :text="tlsChosen ? t('interface.tlsHostHint') : plainChosen ? t('interface.plainHostHint') : t('interface.wsHostHint')" /></label>
                  <label class="ainput block" :class="{ invalid: fieldName === 'transportWs.host' }">
                    <input id="if-ws-host" v-model.trim="form.ws.host" data-field="transportWs.host" class="ltr" :placeholder="tlsChosen ? 'edge.example.com' : 'cdn.example.com'" @input="clearError('transportWs.host')" />
                  </label>
                  <p v-if="fieldName === 'transportWs.host'" class="field-error" role="alert">{{ fieldError }}</p>
                </div>
              </div>
              <div v-if="frontChosen" class="acol12">
                <div class="aform-item">
                  <label class="aform-label" for="if-ws-front-port">{{ t('interface.wsFrontPort') }} <HelpTip :text="plainChosen ? t('interface.plainFrontPortHint') : t('interface.wsFrontPortHint')" /></label>
                  <label class="ainput number" :class="{ invalid: fieldName === 'transportWs.frontPort' }">
                    <input id="if-ws-front-port" v-model="form.ws.frontPort" data-field="transportWs.frontPort" type="number" min="1" max="65535" class="ltr" :placeholder="plainChosen ? '80' : '443'" @input="clearError('transportWs.frontPort')" />
                  </label>
                  <p v-if="fieldName === 'transportWs.frontPort'" class="field-error" role="alert">{{ fieldError }}</p>
                </div>
              </div>
            </div>
            <div class="arow16">
              <div class="acol12">
                <div class="aform-item">
                  <label class="aform-label" for="if-ws-path">{{ t('interface.wsPath') }} <HelpTip :text="pollChosen ? t('interface.pollPathHint') : t('interface.wsPathHint')" /></label>
                  <label class="ainput block" :class="{ invalid: fieldName === 'transportWs.path' }">
                    <input id="if-ws-path" v-model.trim="form.ws.path" data-field="transportWs.path" class="ltr" :placeholder="t('interface.wsPathDrawn')" @input="clearError('transportWs.path')" />
                  </label>
                  <p v-if="fieldName === 'transportWs.path'" class="field-error" role="alert">{{ fieldError }}</p>
                </div>
              </div>
              <div v-if="shapeChosen" class="acol12">
                <div class="aform-item">
                  <label class="aform-label" for="if-ws-shape">{{ t('interface.wsShape') }} <HelpTip :text="t('interface.wsShapeHint')" /></label>
                  <div class="aselect" :class="{ invalid: fieldName === 'transportWs.shape' }">
                    <select id="if-ws-shape" v-model="form.ws.shape" data-field="transportWs.shape" @change="clearError('transportWs.shape')">
                      <option value="get-poll">{{ t('interface.shapeGetPoll') }}</option>
                      <option value="put-poll">{{ t('interface.shapePutPoll') }}</option>
                      <option value="get-stream">{{ t('interface.shapeGetStream') }}</option>
                      <option value="post-stream">{{ t('interface.shapePostStream') }}</option>
                    </select>
                  </div>
                  <p v-if="fieldName === 'transportWs.shape'" class="field-error" role="alert">{{ fieldError }}</p>
                </div>
              </div>
              <div class="acol12">
                <div class="aform-item">
                  <label class="aform-label" for="if-ws-origin">{{ t('interface.wsOrigin') }} <HelpTip :text="tlsChosen ? t('interface.tlsOriginHint') : plainChosen ? t('interface.plainOriginHint') : t('interface.wsOriginHint')" /></label>
                  <label class="ainput block" :class="{ invalid: fieldName === 'transportWs.origin' }">
                    <input id="if-ws-origin" v-model.trim="form.ws.origin" data-field="transportWs.origin" class="ltr" placeholder="http://127.0.0.1:8080" :required="plainChosen || tlsChosen" @input="clearError('transportWs.origin')" />
                  </label>
                  <p v-if="fieldName === 'transportWs.origin'" class="field-error" role="alert">{{ fieldError }}</p>
                </div>
              </div>
            </div>
            <div v-if="frontChosen" class="arow16">
              <div class="acol12">
                <div class="aform-item">
                  <label class="aform-label" for="if-ws-proxies">{{ t('interface.wsProxies') }} <HelpTip :text="t('interface.wsProxiesHint')" /></label>
                  <label class="ainput block" :class="{ invalid: fieldName === 'transportWs.proxies' }">
                    <input id="if-ws-proxies" v-model.trim="form.ws.proxies" data-field="transportWs.proxies" class="ltr" :placeholder="t('interface.wsProxiesNone')" @input="clearError('transportWs.proxies')" />
                  </label>
                  <p v-if="fieldName === 'transportWs.proxies'" class="field-error" role="alert">{{ fieldError }}</p>
                </div>
              </div>
              <div class="acol12">
                <div class="aform-item">
                  <label class="aform-label" for="if-ws-header">{{ t('interface.wsClientHeader') }} <HelpTip :text="t('interface.wsClientHeaderHint')" /></label>
                  <label class="ainput block" :class="{ invalid: fieldName === 'transportWs.clientHeader' }">
                    <input id="if-ws-header" v-model.trim="form.ws.clientHeader" data-field="transportWs.clientHeader" class="ltr" placeholder="X-Forwarded-For" @input="clearError('transportWs.clientHeader')" />
                  </label>
                  <p v-if="fieldName === 'transportWs.clientHeader'" class="field-error" role="alert">{{ fieldError }}</p>
                </div>
              </div>
            </div>
            <div v-if="plainChosen" class="arow16">
              <div class="acol12">
                <div class="aform-item">
                  <label class="aform-label" for="if-ws-cookie">{{ t('interface.plainCookie') }} <HelpTip :text="t('interface.plainCookieHint')" /></label>
                  <label class="ainput block" :class="{ invalid: fieldName === 'transportWs.cookie' }">
                    <input id="if-ws-cookie" v-model.trim="form.ws.cookie" data-field="transportWs.cookie" class="ltr" maxlength="32" :placeholder="t('interface.wsPathDrawn')" @input="clearError('transportWs.cookie')" />
                  </label>
                  <p v-if="fieldName === 'transportWs.cookie'" class="field-error" role="alert">{{ fieldError }}</p>
                </div>
              </div>
            </div>
            <template v-if="tlsChosen">
              <div class="aform-item">
                <div class="opt-card" :class="{ on: form.ws.usePanelCert }" data-field="transportWs.usePanelCert">
                  <div class="opt-text">
                    <div class="opt-title">{{ t('interface.tlsPanelCert') }}</div>
                    <div class="opt-desc">{{ t('interface.tlsPanelCertDesc') }}</div>
                  </div>
                  <Toggle v-model="form.ws.usePanelCert" :label="t('interface.tlsPanelCert')" @update:model-value="clearError('transportWs.usePanelCert')" />
                </div>
                <p v-if="fieldName === 'transportWs.usePanelCert'" class="field-error" role="alert">{{ fieldError }}</p>
                <div v-if="tlsCertSource" class="aalert slim" :class="tlsCertSource === 'panel' ? 'success' : 'info'">
                  <AntIcon :name="tlsCertSource === 'panel' ? 'CheckCircleOutlined' : 'InfoCircleOutlined'" />
                  <span>{{ t(tlsCertSource === 'panel' ? 'interface.tlsCertPanel' : 'interface.tlsCertOwn') }}</span>
                </div>
              </div>
              <div class="aform-item">
                <div class="opt-card" :class="{ on: form.ws.proxyProtocol }">
                  <div class="opt-text">
                    <div class="opt-title">{{ t('interface.tlsProxyProtocol') }}</div>
                    <div class="opt-desc">{{ t('interface.tlsProxyProtocolDesc') }}</div>
                  </div>
                  <Toggle v-model="form.ws.proxyProtocol" :label="t('interface.tlsProxyProtocol')" />
                </div>
              </div>
            </template>
          </template>
          </div>

          <!-- L2TP only: the key every device types beside its own sign-in,
               the routers that have no IPsec, and what customers connect
               from -- under one heading, since none of it is anyone else's. -->
          <section v-if="isL2TP" class="l2tp-box" aria-labelledby="if-l2tp">
            <div id="if-l2tp" class="l2tp-head"><AntIcon name="SafetyCertificateOutlined" />{{ t('interface.l2tpSection') }}</div>

            <div class="aform-item">
              <label class="aform-label" for="if-psk">
                {{ t('interface.psk') }} <HelpTip :text="t('interface.pskHint')" />
                <span v-if="pskStrength" class="atag psk-tag" :class="pskStrength.cls">{{ t(pskStrength.key) }}</span>
              </label>
              <div class="acompact">
                <label class="ainput block" :class="{ invalid: fieldName === 'psk' }">
                  <input
                    id="if-psk"
                    v-model="form.psk"
                    data-field="psk"
                    class="mono"
                    :dir="form.psk || store.locale !== 'fa' ? 'ltr' : 'rtl'"
                    autocomplete="off"
                    spellcheck="false"
                    maxlength="64"
                    :placeholder="t('interface.pskAuto')"
                    @input="clearError('psk')"
                  />
                </label>
                <button v-if="form.psk" type="button" class="abtn icon" :title="t('action.copy')" :aria-label="t('action.copy')" @click="copy(form.psk)"><AntIcon name="CopyOutlined" /></button>
                <button type="button" class="abtn icon" :title="t('client.generate')" :aria-label="t('client.generate')" @click="form.psk = randomPSK(); clearError('psk')"><AntIcon name="ReloadOutlined" /></button>
              </div>
              <p v-if="fieldName === 'psk'" class="field-error" role="alert">{{ fieldError }}</p>
              <Transition name="reveal">
                <div v-if="fieldName !== 'psk' && form.psk && form.psk.length < WEAK_PSK" class="aalert warning slim"><AntIcon name="WarningOutlined" /><span>{{ t('interface.pskWeak') }}</span></div>
              </Transition>
              <Transition name="reveal">
                <div v-if="fieldName !== 'psk' && form.psk.length > 32" class="aalert info slim"><AntIcon name="InfoCircleOutlined" /><span>{{ t('interface.pskLong') }}</span></div>
              </Transition>
            </div>

            <!-- Routers whose L2TP client has no IPsec. Off unless turned on,
                 and while it is on the form says what it costs. -->
            <div class="aform-item">
              <div class="opt-card" :class="{ on: form.l2tpPlain }" data-field="l2tpPlain" tabindex="-1">
                <div class="opt-text">
                  <div class="opt-title">{{ t('interface.l2tpPlain') }} <HelpTip :text="t('interface.l2tpPlainHint')" /></div>
                  <div class="opt-desc">{{ t('interface.l2tpPlainShort') }}</div>
                </div>
                <Toggle v-model="form.l2tpPlain" :label="t('interface.l2tpPlain')" @update:model-value="clearError('l2tpPlain')" />
              </div>
              <p v-if="fieldName === 'l2tpPlain'" class="field-error" role="alert">{{ fieldError }}</p>
              <Transition name="reveal">
                <div v-if="form.l2tpPlain" class="aalert warning slim"><AntIcon name="WarningOutlined" /><span>{{ t('interface.l2tpPlainWarn') }}</span></div>
              </Transition>
            </div>

            <!-- What the customers of this tunnel can connect from: each gets
                 their own device's steps on their page. Folded, since it is
                 something to look up rather than to set. -->
            <div v-if="l2tpDevices.length" class="acollapse dev-collapse">
              <div class="acollapse-item" :class="{ open: devicesOpen }">
                <button type="button" class="acollapse-header" :aria-expanded="devicesOpen" @click="devicesOpen = !devicesOpen">
                  <span class="acollapse-expand"><AntIcon name="RightOutlined" class="dir-icon" /></span>
                  <span class="acollapse-label">{{ t('interface.l2tpDevices') }}</span>
                  <span class="acollapse-extra dev-counts">
                    <span class="atag green"><AntIcon name="LockOutlined" /> {{ ipsecDevices.length }}</span>
                    <span v-if="form.l2tpPlain" class="atag orange"><AntIcon name="WarningOutlined" /> {{ plainDevices.length }}</span>
                  </span>
                </button>
                <Transition name="reveal">
                  <div v-if="devicesOpen" class="acollapse-content">
                    <div class="acollapse-box dev-box">
                      <p class="dev-lead">{{ t('interface.l2tpDevicesLead') }}</p>
                      <div class="dev-group">
                        <div class="dev-label"><AntIcon name="LockOutlined" class="ok" />{{ t('interface.l2tpWithIPsec') }}</div>
                        <div class="dev-tags">
                          <span v-for="d in ipsecDevices" :key="d.id" class="atag" dir="auto">{{ deviceName(d) }}</span>
                        </div>
                      </div>
                      <div class="dev-group" :class="{ off: !form.l2tpPlain }">
                        <div class="dev-label">
                          <AntIcon name="WarningOutlined" class="warn" />{{ t('interface.l2tpWithoutIPsec') }}
                          <span v-if="!form.l2tpPlain" class="dev-off">{{ t('interface.l2tpPlainOffNote') }}</span>
                        </div>
                        <div class="dev-tags">
                          <span v-for="d in plainDevices" :key="d.id" class="atag" dir="auto">{{ deviceName(d) }}</span>
                        </div>
                      </div>
                    </div>
                  </div>
                </Transition>
              </div>
            </div>
          </section>

          <!-- OpenConnect: what Cisco Secure Client dials. -->
          <section v-if="isOC" class="l2tp-box" aria-labelledby="if-oc">
            <div id="if-oc" class="l2tp-head"><AntIcon name="SafetyCertificateOutlined" />{{ t('interface.ocSection') }}</div>
            <div class="aform-item">
              <div class="opt-card" :class="{ on: form.oc.dtls }">
                <div class="opt-text">
                  <div class="opt-title">DTLS</div>
                  <div class="opt-desc">{{ t('interface.ocDtlsDesc') }}</div>
                </div>
                <Toggle v-model="form.oc.dtls" label="DTLS" @update:model-value="clearError('listenPort')" />
              </div>
            </div>
            <div class="aform-item">
              <div class="opt-card" :class="{ on: form.oc.camouflage }">
                <div class="opt-text">
                  <div class="opt-title">{{ t('interface.ocCamouflage') }} <HelpTip :text="t('interface.ocCamouflageHint')" /></div>
                  <div class="opt-desc">{{ t('interface.ocCamouflageDesc') }}</div>
                </div>
                <Toggle v-model="form.oc.camouflage" :label="t('interface.ocCamouflage')" />
              </div>
              <Transition name="reveal">
                <div v-if="form.oc.camouflage" class="acompact oc-secret">
                  <label class="ainput block" :class="{ invalid: fieldName === 'openconnect.camouflageSecret' }">
                    <input v-model="form.oc.camouflageSecret" data-field="openconnect.camouflageSecret" class="mono ltr" autocomplete="off" spellcheck="false" maxlength="64" :placeholder="t('interface.ocSecretAuto')" @input="clearError('openconnect.camouflageSecret')" />
                  </label>
                  <button type="button" class="abtn icon" :title="t('client.generate')" :aria-label="t('client.generate')" @click="drawCamouflage"><AntIcon name="ReloadOutlined" /></button>
                </div>
              </Transition>
              <p v-if="fieldName === 'openconnect.camouflageSecret'" class="field-error" role="alert">{{ fieldError }}</p>
            </div>
            <div class="aform-item">
              <div class="opt-card" :class="{ on: form.oc.usePanelCert }">
                <div class="opt-text">
                  <div class="opt-title">{{ t('interface.ocPanelCert') }}</div>
                  <div class="opt-desc">{{ t('interface.ocPanelCertDesc') }}</div>
                </div>
                <Toggle v-model="form.oc.usePanelCert" :label="t('interface.ocPanelCert')" />
              </div>
              <div v-if="editing && props.iface.openconnect?.certSource" class="aalert slim" :class="props.iface.openconnect.certSource === 'panel' ? 'success' : 'info'">
                <AntIcon :name="props.iface.openconnect.certSource === 'panel' ? 'CheckCircleOutlined' : 'InfoCircleOutlined'" />
                <span>{{ t(props.iface.openconnect.certSource === 'panel' ? 'interface.ocCertPanel' : 'interface.ocCertOwn') }}</span>
              </div>
            </div>
          </section>

          <!-- IKEv2: the ways a device may sign in, each a type in its own
               VPN settings. -->
          <section v-if="isIKEv2" class="l2tp-box" aria-labelledby="if-ikev2">
            <div id="if-ikev2" class="l2tp-head"><AntIcon name="SafetyCertificateOutlined" />{{ t('interface.ikev2Section') }}</div>
            <div class="aform-item" data-field="ikev2Modes" tabindex="-1">
              <div v-for="m in IKE_MODES" :key="m" class="opt-card ike-mode" :class="{ on: form.ikev2Modes.includes(m) }">
                <div class="opt-text">
                  <div class="opt-title">{{ t(`interface.ikev2Mode.${m}`) }}</div>
                  <div class="opt-desc">{{ t(`interface.ikev2ModeDesc.${m}`) }}</div>
                </div>
                <Toggle
                  :model-value="form.ikev2Modes.includes(m)"
                  :label="t(`interface.ikev2Mode.${m}`)"
                  :disabled="form.ikev2Modes.length === 1 && form.ikev2Modes.includes(m)"
                  @update:model-value="(on) => toggleIkeMode(m, on)"
                />
              </div>
              <p v-if="fieldName === 'ikev2Modes'" class="field-error" role="alert">{{ fieldError }}</p>
              <div class="aalert info slim"><AntIcon name="InfoCircleOutlined" /><span>{{ t('interface.ikev2Note') }}</span></div>
            </div>
          </section>

          <!-- PPTP: said plainly before it is made, and every time it is
               opened, so it is never chosen by accident. -->
          <div v-if="isPPTP" class="aalert warning slim pptp-warn"><AntIcon name="WarningOutlined" /><span>{{ t('interface.pptpWarn') }}</span></div>

          <!-- Port, subnet and mode are baked into every config already handed
               out: changing them is allowed, and said to cost every customer a
               new configuration, so it is done knowingly. -->
          <div v-if="editing" class="aalert info edit-note"><AntIcon name="InfoCircleOutlined" /><span>{{ t('interface.editHint') }}</span></div>
        </form>
      </div>

      <div class="amodal-foot if-foot">
        <span class="if-status"><span class="if-status-dot" :class="{ busy }"></span>{{ busy ? t('interface.formSaving') : t('interface.formReady') }}</span>
        <div class="if-foot-actions">
          <button class="abtn" type="button" @click="emit('close')">{{ t('action.cancel') }}</button>
          <button class="abtn primary" type="submit" form="iface-form" :disabled="busy">
            <AntIcon v-if="busy" name="LoadingOutlined" class="spin" />
            <template v-else><AntIcon v-if="!editing" name="PlusOutlined" />{{ editing ? t('action.save') : t('action.create') }}</template>
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.amodal.w720 { width: min(720px, calc(100vw - 32px)); }
/* The rows pull 8px past the body's edges for their gutter; the body is
   widened by the same so nothing scrolls sideways or is clipped. */
.if-body { overflow-x: hidden; padding-inline: 8px; margin-inline: -8px; }
.if-form { padding-top: 4px; }

.arow16 { display: grid; grid-template-columns: repeat(24, minmax(0, 1fr)); margin-inline: -8px; }
.arow16 > [class^='acol'] { padding-inline: 8px; min-width: 0; }
.acol24 { grid-column: 1 / -1; }
.acol12 { grid-column: span 12; }
.acol6 { grid-column: span 6; }
@media (max-width: 768px) {
  .acol12 { grid-column: 1 / -1; }
  .acol6 { grid-column: span 12; }
  .amodal.w720 { width: calc(100vw - 16px); padding: 16px; max-height: calc(100dvh - 32px); }
  .amodal-backdrop { padding: 16px 8px; align-items: flex-start; }
}

.ainput.invalid, .ainput.invalid:hover, .aselect.invalid { border-color: var(--bad); }
.ainput-suffix { margin-inline-start: 4px; color: var(--faint); font-size: 13px; }
.ainput.disabled input { cursor: default; }
.acompact .abtn.icon { width: 32px; padding: 0; flex: none; }
.abtn.icon { display: inline-flex; align-items: center; justify-content: center; height: 32px; }
.mono { font-family: var(--mono); letter-spacing: 0.02em; }
.mono::placeholder { font-family: var(--font); letter-spacing: normal; }

/* The protocol as a segmented choice, the width of its column. */
.aradio-group.proto { display: flex; width: 100%; }
.aradio-group.proto .aradio-btn { flex: 1 1 0; min-width: 0; padding: 0 10px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.aradio-btn:disabled { color: var(--faint); background: var(--surface-2); cursor: not-allowed; }

.enabled-line { display: flex; align-items: center; gap: 8px; }
.enabled-text { font-size: 14px; color: var(--ink); }

/* The header: an icon badge and the title on the left, the close button hard
   against the right edge. */
.if-head { align-items: center; justify-content: space-between; gap: 12px; }
.if-head .amodal-close { align-self: center; flex: none; margin-inline-start: auto; }
.if-head-main { display: flex; align-items: center; gap: 12px; min-width: 0; flex: 1; }
.if-head-badge {
  display: inline-flex; align-items: center; justify-content: center;
  width: 36px; height: 36px; flex: none;
  border-radius: 9px;
  background: var(--accent-soft, var(--surface-2));
  border: 1px solid var(--accent-line, var(--line));
  color: var(--accent-text);
  font-size: 18px;
}
.if-head-text { min-width: 0; }
.if-head-sub { margin: 2px 0 0; font-size: 12px; line-height: 16px; color: var(--faint); }
html[lang='fa'] .if-head-sub { unicode-bidi: plaintext; }

/* A section's heading: a small upper-case label and a rule to the edge. */
.section-head {
  display: flex; align-items: center; gap: 12px;
  margin: 4px 0 14px;
  font-size: 11px; font-weight: 600; letter-spacing: 0.06em; text-transform: uppercase;
  color: var(--faint);
}
.section-head:not(:first-child) { margin-top: 20px; }
.section-rule { height: 1px; flex: 1; background: var(--line); }

/* The advanced card gathers mode and transport, set off like the protocol
   sections below it. The rows inside give up their gutter to the padding. */
.adv-box {
  margin-bottom: 20px;
  padding: 16px;
  border: 1px solid var(--line);
  border-radius: 10px;
  background: var(--surface-2);
}
.adv-box .arow16 { margin-inline: 0; }
.adv-box .arow16 > [class^='acol'] { padding-inline: 0; }
.adv-box .arow16 > [class^='acol']:first-child { padding-inline-end: 8px; }
.adv-box .arow16 > [class^='acol']:last-child { padding-inline-start: 8px; }
.adv-box .ainput, .adv-box .aselect, .adv-box .opt-card { background: var(--surface); }
.adv-box > .arow16:last-child .aform-item, .adv-box > .aform-item:last-child { margin-bottom: 0; }
.adv-head {
  display: flex; align-items: center; justify-content: space-between;
  margin-bottom: 14px;
}
.adv-head-main { display: inline-flex; align-items: center; gap: 8px; font-size: 11px; font-weight: 600; letter-spacing: 0.06em; text-transform: uppercase; color: var(--faint); }
.adv-head-main .adv-ic { color: var(--accent-text); flex: none; }
.adv-head-tag {
  font-size: 10px; letter-spacing: 0.04em; text-transform: uppercase;
  color: var(--faint); background: var(--surface);
  border: 1px solid var(--line);
  padding: 2px 8px; border-radius: 5px;
  font-family: var(--mono);
}
html[lang='fa'] .adv-head-tag { font-family: inherit; }

/* A line of grey helper text under a field, where no error is shown. */
.field-hint { margin: 5px 0 0; font-size: 11px; line-height: 15px; color: var(--faint); }
html[lang='fa'] .field-hint { unicode-bidi: plaintext; }

/* An icon tucked inside an input or select, with the control padded to clear
   it. The page is not mirrored, so the icon stays on the left. */
.ainput.has-ic, .aselect.has-ic { position: relative; }
.ainput.has-ic > .in-ic, .aselect.has-ic > .in-ic {
  position: absolute; left: 11px; top: 50%; transform: translateY(-50%);
  display: inline-flex; align-items: center; justify-content: center;
  color: var(--faint); font-size: 15px; pointer-events: none; z-index: 1;
}
.aselect.has-ic > .in-ic { left: 12px; }
.in-ic.ok { color: var(--tag-green-ink); }
.in-ic.mono-ic { font-family: var(--mono); font-size: 13px; }
.ainput.has-ic > input { padding-inline-start: 34px; }
.aselect.has-ic > select { padding-inline-start: 36px; }

/* The "Kernel Accel" badge at the end of the protocol label. */
.proto-label { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
.proto-badge {
  font-size: 10px; letter-spacing: 0.02em;
  color: var(--tag-green-ink);
  background: var(--tag-green-bg, color-mix(in srgb, var(--tag-green-ink) 12%, transparent));
  border: 1px solid var(--tag-green-line, color-mix(in srgb, var(--tag-green-ink) 30%, transparent));
  padding: 1px 7px; border-radius: 5px;
  font-family: var(--mono); font-weight: 500;
}
html[lang='fa'] .proto-badge { font-family: inherit; }

/* The footer: a status on one side, the actions on the other. */
.if-foot { justify-content: space-between; }
.if-foot-actions { display: inline-flex; align-items: center; gap: 12px; }
.if-status { display: inline-flex; align-items: center; gap: 8px; font-size: 12px; color: var(--muted); }
.if-status-dot { width: 8px; height: 8px; border-radius: 50%; background: var(--tag-green-ink, #22c55e); box-shadow: 0 0 0 3px color-mix(in srgb, var(--tag-green-ink, #22c55e) 20%, transparent); }
.if-status-dot.busy { background: var(--accent-text); box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent-text) 20%, transparent); }
@media (max-width: 520px) {
  .if-status { display: none; }
}

.field-error { margin: 4px 0 0; font-size: 12px; color: var(--bad); }

/* The settings only an L2TP tunnel has, set off as one block. */
.l2tp-box {
  margin-bottom: 24px;
  padding: 16px 16px 0;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--surface-2);
}
.l2tp-box > .aform-item:last-child { margin-bottom: 16px; }
.l2tp-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 16px;
  font-size: 14px;
  font-weight: 600;
  color: var(--ink);
}
.l2tp-head .anticon { color: var(--accent-text); font-size: 16px; }
.l2tp-box .ainput, .l2tp-box .abtn { background: var(--surface); }
.psk-tag { margin-inline: 6px 0; vertical-align: 1px; }

.aalert.slim { margin-top: 8px; padding: 6px 12px; font-size: 13px; line-height: 20px; align-items: flex-start; }
.aalert.slim > .anticon { margin-top: 3px; line-height: 1; }
.pptp-warn { margin: 0 0 24px; }
.ike-mode { margin-bottom: 8px; }
.oc-secret { margin-top: 8px; }
.awg-wait { display: flex; align-items: center; gap: 8px; margin: 0 0 24px; color: var(--muted); }
.awg-wait .spin { animation: awg-wait-spin 0.8s linear infinite; }
@keyframes awg-wait-spin { to { transform: rotate(360deg); } }
.edit-note { margin-bottom: 8px; font-size: 13px; line-height: 20px; align-items: flex-start; }
.edit-note > .anticon { margin-top: 3px; line-height: 1; }
.if-form > .aalert.error { margin-bottom: 16px; }

/* An option with its own explanation: the switch at the end of the line. */
.opt-card {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 12px 14px;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--surface);
  transition: border-color 0.2s, background 0.2s;
  outline: none;
}
.opt-card.on { border-color: var(--tag-orange-line); }
.opt-text { flex: 1; min-width: 0; }
.opt-title { font-size: 14px; line-height: 22px; color: var(--ink); }
.opt-desc { margin-top: 2px; font-size: 12px; line-height: 18px; color: var(--faint); }

.dev-collapse { margin-bottom: 16px; background: var(--surface); }
.dev-collapse .acollapse-header { align-items: center; padding: 10px 14px; }
.dev-collapse .acollapse-label { font-size: 14px; }
.dev-counts { display: inline-flex; gap: 4px; }
.dev-counts .atag { margin: 0; }
[dir='rtl'] .dir-icon { transform: scaleX(-1); }
[dir='rtl'] .acollapse-item.open .dir-icon { transform: rotate(90deg); }
.dev-box { display: grid; gap: 14px; padding: 14px; }
.dev-lead { margin: 0; font-size: 12px; line-height: 18px; color: var(--faint); }
.dev-group { display: grid; gap: 8px; transition: opacity 0.2s; }
.dev-group.off { opacity: 0.5; }
.dev-label { display: flex; align-items: center; flex-wrap: wrap; gap: 6px; font-size: 13px; font-weight: 500; color: var(--ink); }
.dev-label .ok { color: var(--tag-green-ink); }
.dev-label .warn { color: var(--tag-orange-ink); }
.dev-off { font-weight: 400; font-size: 12px; color: var(--faint); }
.dev-tags { display: flex; flex-wrap: wrap; gap: 6px; }
.dev-tags .atag { margin: 0; }

/* The page is not mirrored in Persian; each run of text takes its own
   direction, as the panel's labels and paragraphs do. */
html[lang='fa'] .opt-title, html[lang='fa'] .opt-desc, html[lang='fa'] .dev-lead, html[lang='fa'] .dev-label { unicode-bidi: plaintext; }

/* Things that appear and go fade and slide rather than jump. */
.reveal-enter-active, .reveal-leave-active { transition: opacity 0.2s ease, transform 0.2s ease; }
.reveal-enter-from, .reveal-leave-to { opacity: 0; transform: translateY(-4px); }

.anticon.spin { animation: aspin 1s linear infinite; }
@keyframes aspin { to { transform: rotate(360deg); } }

@media (max-width: 480px) {
  .opt-card { padding: 10px 12px; gap: 12px; }
  .dev-counts { display: none; }
}
</style>
