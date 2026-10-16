<script setup>
// An inbound for a 3x-ui or Pasarguard node, with the fields that panel's
// own form has: the kinds its version serves, each protocol's settings, the
// transport and its settings, TLS or Reality, sniffing, and on 3x-ui the
// inbound's own allowance and expiry, on Pasarguard the host its users'
// links are written for. Keys are drawn by the panel (/api/xray/keys).
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { api } from '../lib/api.js'
import { t } from '../lib/store.js'
import HelpTip from './HelpTip.vue'
import {
  ALPNS, DEFAULT_PORTS, FINGERPRINTS, KCP_HEADERS, SS_METHODS, TRAFFIC_RESETS, XHTTP_MODES, XRAY_NETWORKS,
  buildInbound, newInboundState, parseAdvanced,
} from '../lib/xrayInbound.js'

const props = defineProps({
  node: { type: Object, required: true },
  port: { type: [Number, String], default: 443 },
})
const emit = defineEmits(['port', 'name', 'error'])

// A label in the operator's language when there is one, the panel's own
// English name of the field otherwise -- the name the xray documentation and
// both panels use.
const L = (key, en) => {
  const v = t(key)
  return v === key ? en : v
}

const panel = computed(() => props.node.kind)
const x = reactive(newInboundState())
const kinds = ref([])
const version = ref('')
const loadError = ref('')
const kind = computed(() => kinds.value.find((k) => k.protocol === x.protocol))

async function loadKinds() {
  loadError.value = ''
  try {
    const r = await api.get(`/api/nodes/${props.node.id}/inbound-kinds`)
    kinds.value = r.kinds || []
    version.value = r.version || ''
    // Chosen even when it is already the protocol, so the name and port
    // are its own and not those the form held for this panel's tunnels.
    if (kinds.value.length) choose(kinds.value.some((k) => k.protocol === x.protocol) ? x.protocol : kinds.value[0].protocol)
  } catch (e) {
    loadError.value = e.message
  }
}
onMounted(loadKinds)
watch(() => props.node.id, loadKinds)

// A protocol chosen starts from what its panel's form starts from: its port,
// a transport and security it can have, its keys drawn.
function choose(p) {
  x.protocol = p
  emit('port', DEFAULT_PORTS[p] || 443)
  emit('name', `${p === 'shadowsocks' ? 'ss' : p}-${DEFAULT_PORTS[p] || 443}`)
  const k = kinds.value.find((v) => v.protocol === p)
  if (k?.transports?.length && !k.transports.includes(x.network)) x.network = k.transports[0]
  if (k?.securities?.length && !k.securities.includes(x.security)) x.security = k.securities[0]
  if (p === 'vless' && k?.securities?.includes('reality')) x.security = 'reality'
  if (p === 'hysteria' || p === 'masque') x.security = 'tls'
  drawFor(p)
}

const securities = computed(() => {
  const all = kind.value?.securities || []
  // Reality is over TCP, gRPC and XHTTP alone.
  return all.filter((s) => s !== 'reality' || ['tcp', 'grpc', 'xhttp'].includes(x.network))
})
watch(() => x.network, () => {
  if (!securities.value.includes(x.security)) x.security = securities.value[0] || 'none'
})
watch(() => x.security, (s) => {
  if (s === 'reality' && !x.reality.privateKey) draw('reality')
})
watch(() => x.ss.method, () => {
  if (x.protocol === 'shadowsocks') draw('shadowsocks')
})

const drawing = ref('')
async function draw(kindName) {
  drawing.value = kindName
  try {
    const k = await api.post('/api/xray/keys', { kind: kindName, method: x.ss.method, domain: x.mt.fakeTlsDomain })
    if (kindName === 'reality') {
      x.reality.privateKey = k.privateKey
      x.reality.publicKey = k.publicKey
      if (!x.reality.shortIds) x.reality.shortIds = k.shortId
    } else if (kindName === 'wireguard') {
      if (x.protocol === 'amneziawg') { x.awg.privateKey = k.privateKey; x.awg.publicKey = k.publicKey } else { x.wg.secretKey = k.privateKey; x.wg.publicKey = k.publicKey }
    } else if (kindName === 'shadowsocks') {
      x.ss.password = k.password
    }
  } catch (e) {
    emit('error', e.message)
  } finally {
    drawing.value = ''
  }
}
async function drawShortId() {
  const k = await api.post('/api/xray/keys', { kind: 'reality' })
  x.reality.shortIds = x.reality.shortIds ? `${x.reality.shortIds}, ${k.shortId}` : k.shortId
}
function drawFor(p) {
  if (p === 'shadowsocks') draw('shadowsocks')
  if (p === 'wireguard' || p === 'amneziawg') draw('wireguard')
  if (x.security === 'reality' && !x.reality.privateKey) draw('reality')
}

function addFallback(rows) { rows.push({ name: '', alpn: '', path: '', dest: '', xver: 0 }) }
function toggle(listRef, v) {
  const i = listRef.indexOf(v)
  if (i >= 0) listRef.splice(i, 1)
  else listRef.push(v)
}

const advError = ref('')
// payload is what the tunnel form sends for this inbound; an error says what
// is wrong with it before anything leaves.
function payload() {
  advError.value = ''
  try {
    parseAdvanced(x.advSettings)
    parseAdvanced(x.advStream)
  } catch {
    advError.value = L('xin.advancedBad', 'The advanced JSON must be an object, as {"key": "value"}.')
    throw new Error(advError.value)
  }
  return buildInbound(x, kind.value, panel.value)
}
defineExpose({ payload })

const usesFallbacks = computed(() => ['vless', 'trojan'].includes(x.protocol) && x.network === 'tcp')
const isPG = computed(() => panel.value === 'pasarguard')
const isXUI = computed(() => panel.value === 'xui')
</script>

<template>
  <div class="xin">
    <div v-if="loadError" class="aalert error" role="alert"><span>{{ loadError }}</span></div>
    <p v-if="version" class="hint xin-version">{{ node.name }} · {{ version }}</p>

    <!-- The kinds that server's version has, and nothing it does not. -->
    <div class="arow16">
      <div class="acol12">
        <div class="aform-item">
          <label class="aform-label required" for="xin-proto">{{ t('client.protocol') }}</label>
          <div class="aselect">
            <select id="xin-proto" :value="x.protocol" data-field="protocol" @change="choose($event.target.value)">
              <option v-for="k in kinds" :key="k.protocol" :value="k.protocol">
                {{ L(`protocol.${k.protocol}`, k.protocol) }}{{ k.customers ? '' : ` — ${L('xin.noCustomers', 'no customers')}` }}
              </option>
            </select>
          </div>
          <p v-if="kind && !kind.customers" class="hint">{{ L('xin.noCustomersHint', 'An inbound of this kind has no client per user that panel counts: it is made, but customers cannot be put on it.') }}</p>
        </div>
      </div>
      <div class="acol12">
        <div class="aform-item">
          <label class="aform-label" for="xin-listen">Listen IP <HelpTip :text="L('xin.listenHint', 'Empty listens on every address of that server.')" /></label>
          <label class="ainput block"><input id="xin-listen" v-model="x.listen" class="ltr" placeholder="0.0.0.0" /></label>
        </div>
      </div>
    </div>

    <!-- 3x-ui keeps an allowance, an expiry and a reset on the inbound itself. -->
    <template v-if="isXUI">
      <div class="adivider"><span class="adivider-text">{{ L('xin.limits', 'Inbound traffic and expiry') }}</span></div>
      <div class="arow16">
        <div class="acol6">
          <div class="aform-item">
            <label class="aform-label" for="xin-total">{{ L('xin.totalGB', 'Total (GB)') }} <HelpTip :text="L('xin.totalHint', 'The inbound’s own traffic allowance on that panel, every client on it counted. 0 is no limit.')" /></label>
            <label class="ainput number"><input id="xin-total" v-model="x.totalGB" type="number" min="0" step="0.1" class="ltr" /></label>
          </div>
        </div>
        <div class="acol6">
          <div class="aform-item">
            <label class="aform-label" for="xin-exp">{{ L('xin.expiry', 'Expiry') }}</label>
            <label class="ainput block"><input id="xin-exp" v-model="x.expiry" type="datetime-local" class="ltr" /></label>
          </div>
        </div>
        <div class="acol6">
          <div class="aform-item">
            <label class="aform-label" for="xin-reset">{{ L('xin.trafficReset', 'Traffic reset') }}</label>
            <div class="aselect"><select id="xin-reset" v-model="x.trafficReset" data-field="xray.trafficReset">
              <option v-for="r in TRAFFIC_RESETS" :key="r" :value="r">{{ L(`xin.reset.${r}`, r) }}</option>
            </select></div>
          </div>
        </div>
        <div v-if="x.trafficReset === 'monthly'" class="acol6">
          <div class="aform-item">
            <label class="aform-label" for="xin-rday">{{ L('xin.resetDay', 'Day of month') }}</label>
            <label class="ainput number"><input id="xin-rday" v-model="x.trafficResetDay" type="number" min="1" max="31" class="ltr" /></label>
          </div>
        </div>
      </div>
    </template>

    <!-- The protocol's own settings. -->
    <div class="adivider"><span class="adivider-text">{{ L(`protocol.${x.protocol}`, x.protocol) }}</span></div>

    <template v-if="x.protocol === 'vless'">
      <div class="arow16">
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-dec">Decryption</label>
          <label class="ainput block"><input id="xin-dec" v-model="x.vless.decryption" class="ltr" placeholder="none" /></label></div></div>
        <div v-if="isXUI" class="acol12"><div class="aform-item"><label class="aform-label" for="xin-enc">Encryption <HelpTip :text="L('xin.vlessEncHint', 'VLESS encryption for clients; none unless the decryption above is set to match.')" /></label>
          <label class="ainput block"><input id="xin-enc" v-model="x.vless.encryption" class="ltr" placeholder="none" /></label></div></div>
      </div>
    </template>

    <template v-if="x.protocol === 'shadowsocks'">
      <div class="arow16">
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-ssm">{{ L('interface.xrayMethod', 'Cipher') }}</label>
          <div class="aselect"><select id="xin-ssm" v-model="x.ss.method" data-field="xray.method"><option v-for="m in SS_METHODS" :key="m" :value="m">{{ m }}</option></select></div></div></div>
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-ssp">Password <HelpTip :text="L('xin.ssPassHint', 'The server key; a 2022 cipher needs one of its size, drawn here.')" /></label>
          <div class="xin-keyrow"><label class="ainput block"><input id="xin-ssp" v-model="x.ss.password" class="ltr mono" /></label>
            <button type="button" class="abtn" :disabled="drawing === 'shadowsocks'" @click="draw('shadowsocks')">{{ L('xin.draw', 'Draw') }}</button></div></div></div>
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-ssn">Network</label>
          <div class="aselect"><select id="xin-ssn" v-model="x.ss.network"><option value="tcp,udp">TCP, UDP</option><option value="tcp">TCP</option><option value="udp">UDP</option></select></div></div></div>
        <div class="acol12"><label class="acheckbox xin-check"><input v-model="x.ss.ivCheck" type="checkbox" /><span>IV check</span></label></div>
      </div>
    </template>

    <template v-if="x.protocol === 'hysteria'">
      <div class="arow16">
        <div class="acol6"><div class="aform-item"><label class="aform-label" for="xin-hyc">Congestion</label>
          <div class="aselect"><select id="xin-hyc" v-model="x.hy.congestion"><option value="">bbr ({{ L('xin.default', 'default') }})</option><option value="brutal">brutal</option></select></div></div></div>
        <div class="acol6"><div class="aform-item"><label class="aform-label" for="xin-hyu">Up</label>
          <label class="ainput block"><input id="xin-hyu" v-model="x.hy.up" class="ltr" placeholder="100 mbps" /></label></div></div>
        <div class="acol6"><div class="aform-item"><label class="aform-label" for="xin-hyd">Down</label>
          <label class="ainput block"><input id="xin-hyd" v-model="x.hy.down" class="ltr" placeholder="100 mbps" /></label></div></div>
        <div class="acol6"><div class="aform-item"><label class="aform-label" for="xin-hyi">UDP idle timeout (s)</label>
          <label class="ainput number"><input id="xin-hyi" v-model="x.hy.udpIdleTimeout" type="number" min="0" class="ltr" /></label></div></div>
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-hyo">Obfs (salamander) password <HelpTip :text="L('xin.hyObfsHint', 'Empty is no obfuscation.')" /></label>
          <label class="ainput block"><input id="xin-hyo" v-model="x.hy.obfs" class="ltr" /></label></div></div>
      </div>
    </template>

    <template v-if="x.protocol === 'tuic'">
      <div class="arow16">
        <div class="acol12"><div class="aform-item"><label class="aform-label required" for="xin-tc">Certificate</label>
          <label class="ainput block"><input id="xin-tc" v-model="x.tuic.certificate" class="ltr" placeholder="/etc/ssl/fullchain.pem" /></label></div></div>
        <div class="acol12"><div class="aform-item"><label class="aform-label required" for="xin-tk">Private key</label>
          <label class="ainput block"><input id="xin-tk" v-model="x.tuic.private_key" class="ltr" placeholder="/etc/ssl/privkey.pem" /></label></div></div>
        <div class="acol6"><div class="aform-item"><label class="aform-label" for="xin-tcc">Congestion control</label>
          <div class="aselect"><select id="xin-tcc" v-model="x.tuic.congestion_control"><option>bbr</option><option>cubic</option><option>new_reno</option></select></div></div></div>
        <div class="acol6"><div class="aform-item"><label class="aform-label" for="xin-tur">UDP relay mode</label>
          <div class="aselect"><select id="xin-tur" v-model="x.tuic.udp_relay_mode"><option>native</option><option>quic</option></select></div></div></div>
        <div class="acol6"><div class="aform-item"><label class="aform-label" for="xin-tmi">Max idle time (s)</label>
          <label class="ainput number"><input id="xin-tmi" v-model="x.tuic.max_idle_time" type="number" min="1" class="ltr" /></label></div></div>
        <div class="acol6"><div class="aform-item"><label class="aform-label" for="xin-tat">Auth timeout (s)</label>
          <label class="ainput number"><input id="xin-tat" v-model="x.tuic.authentication_timeout" type="number" min="1" class="ltr" /></label></div></div>
        <div class="acol6"><div class="aform-item"><label class="aform-label" for="xin-tmu">Max UDP packet</label>
          <label class="ainput number"><input id="xin-tmu" v-model="x.tuic.max_udp_relay_packet_size" type="number" min="1" max="65245" class="ltr" /></label></div></div>
        <div class="acol6"><div class="aform-item"><label class="aform-label" for="xin-tsn">SNI</label>
          <label class="ainput block"><input id="xin-tsn" v-model="x.tuic.sni" class="ltr" /></label></div></div>
        <div class="acol12"><div class="aform-item"><label class="aform-label">ALPN</label>
          <div class="xin-chips"><label v-for="a in ALPNS" :key="a" class="acheckbox"><input type="checkbox" :checked="x.tuic.alpn.includes(a)" @change="toggle(x.tuic.alpn, a)" /><span>{{ a }}</span></label></div></div></div>
        <div class="acol12"><label class="acheckbox xin-check"><input v-model="x.tuic.zero_rtt_handshake" type="checkbox" /><span>0-RTT handshake</span></label></div>
      </div>
    </template>

    <template v-if="x.protocol === 'mtproto'">
      <div class="arow16">
        <div class="acol12"><div class="aform-item"><label class="aform-label required" for="xin-md">Fake TLS domain</label>
          <label class="ainput block"><input id="xin-md" v-model="x.mt.fakeTlsDomain" class="ltr" /></label></div></div>
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-mp">Prefer IP</label>
          <div class="aselect"><select id="xin-mp" v-model="x.mt.preferIp"><option value="">{{ L('xin.default', 'default') }}</option><option>prefer-ipv4</option><option>prefer-ipv6</option><option>only-ipv4</option><option>only-ipv6</option></select></div></div></div>
        <div class="acol6"><div class="aform-item"><label class="aform-label" for="xin-mt">Max connections</label>
          <label class="ainput number"><input id="xin-mt" v-model="x.mt.throttleMaxConnections" type="number" min="0" class="ltr" /></label></div></div>
        <div class="acol6"><div class="aform-item"><label class="aform-label" for="xin-mh">Pending handshakes per IP</label>
          <label class="ainput number"><input id="xin-mh" v-model="x.mt.pendingHandshakesMaxPerIp" type="number" min="0" class="ltr" /></label></div></div>
        <div class="acol6"><div class="aform-item"><label class="aform-label" for="xin-mfi">Domain fronting IP</label>
          <label class="ainput block"><input id="xin-mfi" v-model="x.mt.frontIp" class="ltr" /></label></div></div>
        <div class="acol6"><div class="aform-item"><label class="aform-label" for="xin-mfp">Domain fronting port</label>
          <label class="ainput number"><input id="xin-mfp" v-model="x.mt.frontPort" type="number" min="0" max="65535" class="ltr" /></label></div></div>
        <div class="acol6"><div class="aform-item"><label class="aform-label" for="xin-m4">Public IPv4</label>
          <label class="ainput block"><input id="xin-m4" v-model="x.mt.publicIpv4" class="ltr" /></label></div></div>
        <div class="acol6"><div class="aform-item"><label class="aform-label" for="xin-m6">Public IPv6</label>
          <label class="ainput block"><input id="xin-m6" v-model="x.mt.publicIpv6" class="ltr" /></label></div></div>
        <div class="acol12 xin-checks">
          <label class="acheckbox"><input v-model="x.mt.frontProxyProtocol" type="checkbox" /><span>Fronting PROXY protocol</span></label>
          <label class="acheckbox"><input v-model="x.mt.proxyProtocolListener" type="checkbox" /><span>PROXY protocol listener</span></label>
        </div>
      </div>
    </template>

    <template v-if="x.protocol === 'wireguard'">
      <div class="arow16">
        <div class="acol24"><div class="aform-item"><label class="aform-label required" for="xin-wk">Secret key</label>
          <div class="xin-keyrow"><label class="ainput block"><input id="xin-wk" v-model="x.wg.secretKey" class="ltr mono" /></label>
            <button type="button" class="abtn" :disabled="drawing === 'wireguard'" @click="draw('wireguard')">{{ L('xin.draw', 'Draw') }}</button></div>
          <p v-if="x.wg.publicKey" class="hint ltr mono">Public key: {{ x.wg.publicKey }}</p></div></div>
        <div class="acol6"><div class="aform-item"><label class="aform-label" for="xin-wm">MTU</label>
          <label class="ainput number"><input id="xin-wm" v-model="x.wg.mtu" type="number" min="576" class="ltr" /></label></div></div>
        <template v-if="isPG">
          <div class="acol18"><div class="aform-item"><label class="aform-label" for="xin-wa">Address</label>
            <label class="ainput block"><input id="xin-wa" v-model="x.wg.address" class="ltr" placeholder="10.0.0.1/24" /></label></div></div>
        </template>
        <template v-else>
          <div class="acol6"><div class="aform-item"><label class="aform-label" for="xin-ws">Subnet</label>
            <label class="ainput block"><input id="xin-ws" v-model="x.wg.subnetIp" class="ltr" /></label></div></div>
          <div class="acol6"><div class="aform-item"><label class="aform-label" for="xin-wc">CIDR</label>
            <label class="ainput number"><input id="xin-wc" v-model="x.wg.subnetCidr" type="number" min="1" max="32" class="ltr" /></label></div></div>
          <div class="acol6"><div class="aform-item"><label class="aform-label" for="xin-wd">DNS</label>
            <label class="ainput block"><input id="xin-wd" v-model="x.wg.dns" class="ltr" placeholder="1.1.1.1" /></label></div></div>
        </template>
        <div class="acol12"><label class="acheckbox xin-check"><input v-model="x.wg.noKernelTun" type="checkbox" /><span>No kernel TUN</span></label></div>
      </div>
    </template>

    <template v-if="x.protocol === 'amneziawg'">
      <div class="arow16">
        <div class="acol24"><div class="aform-item"><label class="aform-label required" for="xin-ak">Private key</label>
          <div class="xin-keyrow"><label class="ainput block"><input id="xin-ak" v-model="x.awg.privateKey" class="ltr mono" /></label>
            <button type="button" class="abtn" :disabled="drawing === 'wireguard'" @click="draw('wireguard')">{{ L('xin.draw', 'Draw') }}</button></div></div></div>
        <div class="acol6"><div class="aform-item"><label class="aform-label" for="xin-asi">Subnet</label><label class="ainput block"><input id="xin-asi" v-model="x.awg.subnetIp" class="ltr" /></label></div></div>
        <div class="acol6"><div class="aform-item"><label class="aform-label" for="xin-asc">CIDR</label><label class="ainput number"><input id="xin-asc" v-model="x.awg.subnetCidr" type="number" min="1" max="32" class="ltr" /></label></div></div>
        <div class="acol6"><div class="aform-item"><label class="aform-label" for="xin-amt">MTU</label><label class="ainput number"><input id="xin-amt" v-model="x.awg.mtu" type="number" class="ltr" /></label></div></div>
        <div class="acol6"><div class="aform-item"><label class="aform-label" for="xin-aei">External interface</label><label class="ainput block"><input id="xin-aei" v-model="x.awg.externalInterface" class="ltr" placeholder="eth0" /></label></div></div>
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-ad1">Primary DNS</label><label class="ainput block"><input id="xin-ad1" v-model="x.awg.primaryDns" class="ltr" /></label></div></div>
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-ad2">Secondary DNS</label><label class="ainput block"><input id="xin-ad2" v-model="x.awg.secondaryDns" class="ltr" /></label></div></div>
        <div v-for="k in ['jc', 'jmin', 'jmax', 's1', 's2', 's3', 's4']" :key="k" class="acol6">
          <div class="aform-item"><label class="aform-label" :for="`xin-a-${k}`">{{ k.toUpperCase() }}</label>
            <label class="ainput number"><input :id="`xin-a-${k}`" v-model="x.awg[k]" type="number" min="0" class="ltr" /></label></div>
        </div>
        <div v-for="k in ['h1', 'h2', 'h3', 'h4', 'i1', 'i2', 'i3', 'i4', 'i5']" :key="k" class="acol6">
          <div class="aform-item"><label class="aform-label" :for="`xin-a-${k}`">{{ k.toUpperCase() }}</label>
            <label class="ainput block"><input :id="`xin-a-${k}`" v-model="x.awg[k]" class="ltr mono" /></label></div>
        </div>
        <div class="acol24 xin-checks">
          <label class="acheckbox"><input v-model="x.awg.ipv6Enabled" type="checkbox" /><span>IPv6</span></label>
          <label class="acheckbox"><input v-model="x.awg.routeThroughXray" type="checkbox" /><span>Route through xray</span></label>
          <label class="acheckbox"><input v-model="x.awg.randomTrailers" type="checkbox" /><span>Random trailers</span></label>
          <label class="acheckbox"><input v-model="x.awg.disableCookies" type="checkbox" /><span>Disable cookies</span></label>
        </div>
        <div v-if="x.awg.ipv6Enabled" class="acol12"><div class="aform-item"><label class="aform-label" for="xin-a6">IPv6 subnet</label><label class="ainput block"><input id="xin-a6" v-model="x.awg.ipv6Subnet" class="ltr" /></label></div></div>
      </div>
    </template>

    <template v-if="x.protocol === 'masque'">
      <div class="arow16">
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-qp">Path</label><label class="ainput block"><input id="xin-qp" v-model="x.mq.path" class="ltr" /></label></div></div>
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-qh">Host</label><label class="ainput block"><input id="xin-qh" v-model="x.mq.host" class="ltr" /></label></div></div>
        <div class="acol18"><div class="aform-item"><label class="aform-label" for="xin-qa">Address pool</label><label class="ainput block"><input id="xin-qa" v-model="x.masque.address" class="ltr" /></label></div></div>
        <div class="acol6"><div class="aform-item"><label class="aform-label" for="xin-qm">MTU</label><label class="ainput number"><input id="xin-qm" v-model="x.masque.mtu" type="number" min="1280" class="ltr" /></label></div></div>
      </div>
    </template>

    <template v-if="x.protocol === 'mixed' || x.protocol === 'socks'">
      <div class="arow16">
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-xa">Auth</label>
          <div class="aselect"><select id="xin-xa" v-model="x.mixed.auth"><option value="password">password</option><option value="noauth">noauth</option></select></div></div></div>
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-xi">UDP IP</label><label class="ainput block"><input id="xin-xi" v-model="x.mixed.ip" class="ltr" /></label></div></div>
        <div v-if="x.mixed.auth === 'password'" class="acol24"><div class="aform-item"><label class="aform-label" for="xin-xc">Accounts <HelpTip :text="L('xin.accountsHint', 'One a line: user:password.')" /></label>
          <textarea id="xin-xc" v-model="x.mixed.accounts" class="atextarea ltr mono" rows="3" spellcheck="false" placeholder="user:password"></textarea></div></div>
        <div class="acol12"><label class="acheckbox xin-check"><input v-model="x.mixed.udp" type="checkbox" /><span>UDP</span></label></div>
      </div>
    </template>

    <template v-if="x.protocol === 'http'">
      <div class="arow16">
        <div class="acol24"><div class="aform-item"><label class="aform-label" for="xin-hc">Accounts <HelpTip :text="L('xin.accountsHint', 'One a line: user:password.')" /></label>
          <textarea id="xin-hc" v-model="x.http.accounts" class="atextarea ltr mono" rows="3" spellcheck="false" placeholder="user:password"></textarea></div></div>
        <div class="acol12"><label class="acheckbox xin-check"><input v-model="x.http.allowTransparent" type="checkbox" /><span>Allow transparent</span></label></div>
      </div>
    </template>

    <template v-if="x.protocol === 'tunnel' || x.protocol === 'dokodemo-door'">
      <div class="arow16">
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-ua">{{ x.protocol === 'tunnel' ? 'Rewrite address' : 'Address' }}</label><label class="ainput block"><input id="xin-ua" v-model="x.tunnel.address" class="ltr" /></label></div></div>
        <div class="acol6"><div class="aform-item"><label class="aform-label" for="xin-up">{{ x.protocol === 'tunnel' ? 'Rewrite port' : 'Port' }}</label><label class="ainput number"><input id="xin-up" v-model="x.tunnel.port" type="number" min="1" max="65535" class="ltr" /></label></div></div>
        <div class="acol6"><div class="aform-item"><label class="aform-label" for="xin-un">Network</label>
          <div class="aselect"><select id="xin-un" v-model="x.tunnel.network"><option value="tcp,udp">TCP, UDP</option><option value="tcp">TCP</option><option value="udp">UDP</option></select></div></div></div>
        <div class="acol12"><label class="acheckbox xin-check"><input v-model="x.tunnel.followRedirect" type="checkbox" /><span>Follow redirect</span></label></div>
      </div>
    </template>

    <!-- Fallbacks, for VLESS and Trojan over TCP. -->
    <template v-if="usesFallbacks">
      <div class="xin-sub">Fallbacks <button type="button" class="abtn small" @click="addFallback(x.protocol === 'vless' ? x.vless.fallbacks : x.trojan.fallbacks)">+</button></div>
      <div v-for="(f, i) in (x.protocol === 'vless' ? x.vless.fallbacks : x.trojan.fallbacks)" :key="i" class="arow16 xin-fallback">
        <div class="acol6"><label class="ainput block"><input v-model="f.name" class="ltr" placeholder="SNI" /></label></div>
        <div class="acol4"><label class="ainput block"><input v-model="f.alpn" class="ltr" placeholder="ALPN" /></label></div>
        <div class="acol4"><label class="ainput block"><input v-model="f.path" class="ltr" placeholder="Path" /></label></div>
        <div class="acol6"><label class="ainput block"><input v-model="f.dest" class="ltr" placeholder="Dest" /></label></div>
        <div class="acol2"><label class="ainput number"><input v-model="f.xver" type="number" min="0" max="2" class="ltr" placeholder="xver" /></label></div>
        <div class="acol2"><button type="button" class="abtn small danger" @click="(x.protocol === 'vless' ? x.vless.fallbacks : x.trojan.fallbacks).splice(i, 1)">×</button></div>
      </div>
    </template>

    <!-- The transport and the security. -->
    <template v-if="kind?.stream && x.protocol !== 'hysteria' && x.protocol !== 'masque'">
      <div class="adivider"><span class="adivider-text">{{ L('interface.xrayTransport', 'Transport') }}</span></div>
      <div class="arow16">
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-net">{{ L('interface.xrayTransport', 'Transport') }}</label>
          <div class="aselect"><select id="xin-net" v-model="x.network" data-field="xray.network"><option v-for="n in kind.transports" :key="n" :value="n">{{ XRAY_NETWORKS[n] || n }}</option></select></div></div></div>
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-sec">{{ L('interface.xraySecurity', 'Security') }}</label>
          <div class="aselect"><select id="xin-sec" v-model="x.security" data-field="xray.security"><option v-for="s in securities" :key="s" :value="s">{{ s === 'none' ? L('interface.xrayNone', 'None') : s === 'tls' ? 'TLS' : 'Reality' }}</option></select></div></div></div>
      </div>

      <div v-if="x.network === 'tcp'" class="arow16">
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-th">Header</label>
          <div class="aselect"><select id="xin-th" v-model="x.tcp.header"><option value="none">none</option><option value="http">HTTP</option></select></div></div></div>
        <template v-if="x.tcp.header === 'http'">
          <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-tp">Request path</label><label class="ainput block"><input id="xin-tp" v-model="x.tcp.paths" class="ltr" /></label></div></div>
          <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-thh">Request host</label><label class="ainput block"><input id="xin-thh" v-model="x.tcp.hosts" class="ltr" /></label></div></div>
        </template>
        <div class="acol12"><label class="acheckbox xin-check"><input v-model="x.tcp.acceptProxyProtocol" type="checkbox" /><span>Accept PROXY protocol</span></label></div>
      </div>
      <div v-if="x.network === 'ws'" class="arow16">
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-wsp">Path</label><label class="ainput block"><input id="xin-wsp" v-model="x.ws.path" class="ltr" /></label></div></div>
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-wsh">Host</label><label class="ainput block"><input id="xin-wsh" v-model="x.ws.host" class="ltr" /></label></div></div>
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-wshb">Heartbeat period (s)</label><label class="ainput number"><input id="xin-wshb" v-model="x.ws.heartbeatPeriod" type="number" min="0" class="ltr" /></label></div></div>
        <div class="acol24"><div class="aform-item"><label class="aform-label" for="xin-wshd">Headers <HelpTip :text="L('xin.headersHint', 'One a line: Name: value.')" /></label>
          <textarea id="xin-wshd" v-model="x.ws.headers" class="atextarea ltr mono" rows="2" spellcheck="false"></textarea></div></div>
        <div class="acol12"><label class="acheckbox xin-check"><input v-model="x.ws.acceptProxyProtocol" type="checkbox" /><span>Accept PROXY protocol</span></label></div>
      </div>
      <div v-if="x.network === 'grpc'" class="arow16">
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-gs">Service name</label><label class="ainput block"><input id="xin-gs" v-model="x.grpc.serviceName" class="ltr" /></label></div></div>
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-ga">Authority</label><label class="ainput block"><input id="xin-ga" v-model="x.grpc.authority" class="ltr" /></label></div></div>
        <div class="acol12"><label class="acheckbox xin-check"><input v-model="x.grpc.multiMode" type="checkbox" /><span>Multi mode</span></label></div>
      </div>
      <div v-if="x.network === 'httpupgrade'" class="arow16">
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-hup">Path</label><label class="ainput block"><input id="xin-hup" v-model="x.hu.path" class="ltr" /></label></div></div>
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-huh">Host</label><label class="ainput block"><input id="xin-huh" v-model="x.hu.host" class="ltr" /></label></div></div>
        <div class="acol24"><div class="aform-item"><label class="aform-label" for="xin-hud">Headers</label>
          <textarea id="xin-hud" v-model="x.hu.headers" class="atextarea ltr mono" rows="2" spellcheck="false"></textarea></div></div>
        <div class="acol12"><label class="acheckbox xin-check"><input v-model="x.hu.acceptProxyProtocol" type="checkbox" /><span>Accept PROXY protocol</span></label></div>
      </div>
      <div v-if="x.network === 'xhttp'" class="arow16">
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-xp">Path</label><label class="ainput block"><input id="xin-xp" v-model="x.xhttp.path" class="ltr" /></label></div></div>
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-xh">Host</label><label class="ainput block"><input id="xin-xh" v-model="x.xhttp.host" class="ltr" /></label></div></div>
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-xm">Mode</label>
          <div class="aselect"><select id="xin-xm" v-model="x.xhttp.mode"><option v-for="m in XHTTP_MODES" :key="m" :value="m">{{ m }}</option></select></div></div></div>
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-xpb">Padding bytes</label><label class="ainput block"><input id="xin-xpb" v-model="x.xhttp.xPaddingBytes" class="ltr" /></label></div></div>
        <div class="acol8"><div class="aform-item"><label class="aform-label" for="xin-xmp">Max each post bytes</label><label class="ainput block"><input id="xin-xmp" v-model="x.xhttp.scMaxEachPostBytes" class="ltr" placeholder="1000000" /></label></div></div>
        <div class="acol8"><div class="aform-item"><label class="aform-label" for="xin-xbp">Max buffered posts</label><label class="ainput number"><input id="xin-xbp" v-model="x.xhttp.scMaxBufferedPosts" type="number" class="ltr" placeholder="30" /></label></div></div>
        <div class="acol8"><div class="aform-item"><label class="aform-label" for="xin-xsu">Stream-up server secs</label><label class="ainput block"><input id="xin-xsu" v-model="x.xhttp.scStreamUpServerSecs" class="ltr" placeholder="20-80" /></label></div></div>
        <div class="acol12"><label class="acheckbox xin-check"><input v-model="x.xhttp.noSSEHeader" type="checkbox" /><span>No SSE header</span></label></div>
      </div>
      <div v-if="x.network === 'kcp'" class="arow16">
        <div v-for="k in ['mtu', 'tti', 'uplinkCapacity', 'downlinkCapacity', 'readBufferSize', 'writeBufferSize']" :key="k" class="acol8">
          <div class="aform-item"><label class="aform-label" :for="`xin-k-${k}`">{{ k }}</label><label class="ainput number"><input :id="`xin-k-${k}`" v-model="x.kcp[k]" type="number" min="0" class="ltr" /></label></div>
        </div>
        <div class="acol8"><div class="aform-item"><label class="aform-label" for="xin-kh">Header</label>
          <div class="aselect"><select id="xin-kh" v-model="x.kcp.header"><option v-for="h in KCP_HEADERS" :key="h" :value="h">{{ h }}</option></select></div></div></div>
        <div class="acol8"><div class="aform-item"><label class="aform-label" for="xin-ks">Seed</label><label class="ainput block"><input id="xin-ks" v-model="x.kcp.seed" class="ltr" /></label></div></div>
        <div class="acol8"><label class="acheckbox xin-check"><input v-model="x.kcp.congestion" type="checkbox" /><span>Congestion</span></label></div>
      </div>
    </template>

    <!-- TLS: a certificate on that server, as a path or as its text. -->
    <template v-if="kind?.stream && (x.security === 'tls' || x.protocol === 'hysteria' || x.protocol === 'masque')">
      <div class="adivider"><span class="adivider-text">TLS</span></div>
      <div class="arow16">
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-tsni">SNI</label><label class="ainput block"><input id="xin-tsni" v-model="x.tls.serverName" class="ltr" placeholder="vpn.example.com" /></label></div></div>
        <div class="acol6"><div class="aform-item"><label class="aform-label" for="xin-tmin">Min version</label>
          <div class="aselect"><select id="xin-tmin" v-model="x.tls.minVersion"><option>1.0</option><option>1.1</option><option>1.2</option><option>1.3</option></select></div></div></div>
        <div class="acol6"><div class="aform-item"><label class="aform-label" for="xin-tmax">Max version</label>
          <div class="aselect"><select id="xin-tmax" v-model="x.tls.maxVersion"><option>1.0</option><option>1.1</option><option>1.2</option><option>1.3</option></select></div></div></div>
        <div v-if="x.protocol !== 'hysteria' && x.protocol !== 'masque'" class="acol24"><div class="aform-item"><label class="aform-label">ALPN</label>
          <div class="xin-chips"><label v-for="a in ALPNS" :key="a" class="acheckbox"><input type="checkbox" :checked="x.tls.alpn.includes(a)" @change="toggle(x.tls.alpn, a)" /><span>{{ a }}</span></label></div></div></div>
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-tcm">{{ L('xin.certificate', 'Certificate') }}</label>
          <div class="aselect"><select id="xin-tcm" v-model="x.tls.certMode"><option value="file">{{ L('xin.certFile', 'Files on that server') }}</option><option value="text">{{ L('xin.certText', 'Pasted here') }}</option></select></div></div></div>
        <div v-if="isXUI" class="acol12"><div class="aform-item"><label class="aform-label" for="xin-tfp">Fingerprint <HelpTip :text="L('xin.fpHint', 'What the clients’ links ask for.')" /></label>
          <div class="aselect"><select id="xin-tfp" v-model="x.tls.fingerprint"><option v-for="f in FINGERPRINTS" :key="f" :value="f">{{ f }}</option></select></div></div></div>
        <template v-if="x.tls.certMode === 'file'">
          <div class="acol12"><div class="aform-item"><label class="aform-label required" for="xin-tcf">Certificate file</label><label class="ainput block" :class="{}"><input id="xin-tcf" v-model="x.tls.certificateFile" data-field="xray.tls.certificates" class="ltr" placeholder="/root/cert/fullchain.pem" /></label></div></div>
          <div class="acol12"><div class="aform-item"><label class="aform-label required" for="xin-tkf">Key file</label><label class="ainput block"><input id="xin-tkf" v-model="x.tls.keyFile" class="ltr" placeholder="/root/cert/privkey.pem" /></label></div></div>
        </template>
        <template v-else>
          <div class="acol12"><div class="aform-item"><label class="aform-label required" for="xin-tct">Certificate</label><textarea id="xin-tct" v-model="x.tls.certificate" data-field="xray.tls.certificates" class="atextarea ltr mono" rows="4" spellcheck="false" placeholder="-----BEGIN CERTIFICATE-----"></textarea></div></div>
          <div class="acol12"><div class="aform-item"><label class="aform-label required" for="xin-tkt">Key</label><textarea id="xin-tkt" v-model="x.tls.key" class="atextarea ltr mono" rows="4" spellcheck="false" placeholder="-----BEGIN PRIVATE KEY-----"></textarea></div></div>
        </template>
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-tcs">Cipher suites</label><label class="ainput block"><input id="xin-tcs" v-model="x.tls.cipherSuites" class="ltr" /></label></div></div>
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-tech">ECH server keys</label><label class="ainput block"><input id="xin-tech" v-model="x.tls.echServerKeys" class="ltr mono" /></label></div></div>
        <div class="acol24 xin-checks">
          <label class="acheckbox"><input v-model="x.tls.rejectUnknownSni" type="checkbox" /><span>Reject unknown SNI</span></label>
          <label v-if="isXUI" class="acheckbox"><input v-model="x.tls.allowInsecure" type="checkbox" /><span>Allow insecure ({{ L('xin.inLinks', 'in links') }})</span></label>
        </div>
      </div>
    </template>

    <!-- Reality: the site whose handshake it borrows, and its keys. -->
    <template v-if="kind?.stream && x.security === 'reality'">
      <div class="adivider"><span class="adivider-text">Reality</span></div>
      <div class="arow16">
        <div class="acol12"><div class="aform-item"><label class="aform-label required" for="xin-rt">Target <HelpTip :text="L('interface.xraySniHint', 'A site reachable from the customers that serves TLS 1.3.')" /></label>
          <label class="ainput block"><input id="xin-rt" v-model="x.reality.target" data-field="xray.reality.target" class="ltr" placeholder="www.speedtest.net:443" /></label></div></div>
        <div class="acol12"><div class="aform-item"><label class="aform-label required" for="xin-rsn">Server names</label>
          <label class="ainput block"><input id="xin-rsn" v-model="x.reality.serverNames" data-field="xray.reality.serverNames" class="ltr" placeholder="www.speedtest.net" /></label></div></div>
        <div class="acol24"><div class="aform-item"><label class="aform-label required" for="xin-rpk">Private key</label>
          <div class="xin-keyrow"><label class="ainput block"><input id="xin-rpk" v-model="x.reality.privateKey" data-field="xray.reality.privateKey" class="ltr mono" /></label>
            <button type="button" class="abtn" :disabled="drawing === 'reality'" @click="draw('reality')">{{ L('xin.draw', 'Draw') }}</button></div>
          <p v-if="x.reality.publicKey" class="hint ltr mono">Public key: {{ x.reality.publicKey }}</p></div></div>
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-rsi">Short IDs</label>
          <div class="xin-keyrow"><label class="ainput block"><input id="xin-rsi" v-model="x.reality.shortIds" class="ltr mono" /></label>
            <button type="button" class="abtn" @click="drawShortId">+</button></div></div></div>
        <div class="acol6"><div class="aform-item"><label class="aform-label" for="xin-rx">Xver</label><label class="ainput number"><input id="xin-rx" v-model="x.reality.xver" type="number" min="0" max="2" class="ltr" /></label></div></div>
        <div class="acol6"><div class="aform-item"><label class="aform-label" for="xin-rmt">Max time diff (ms)</label><label class="ainput number"><input id="xin-rmt" v-model="x.reality.maxTimeDiff" type="number" min="0" class="ltr" /></label></div></div>
        <template v-if="isXUI">
          <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-rfp">Fingerprint</label>
            <div class="aselect"><select id="xin-rfp" v-model="x.reality.fingerprint"><option v-for="f in FINGERPRINTS" :key="f" :value="f">{{ f }}</option></select></div></div></div>
          <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-rsx">SpiderX</label><label class="ainput block"><input id="xin-rsx" v-model="x.reality.spiderX" class="ltr" /></label></div></div>
        </template>
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-rml">ML-DSA-65 seed <HelpTip :text="L('xin.mldsaHint', 'Optional: post-quantum signature of the handshake.')" /></label><label class="ainput block"><input id="xin-rml" v-model="x.reality.mldsa65Seed" class="ltr mono" /></label></div></div>
        <div class="acol12"><label class="acheckbox xin-check"><input v-model="x.reality.show" type="checkbox" /><span>Show (debug)</span></label></div>
      </div>
    </template>

    <!-- Sniffing. -->
    <template v-if="kind?.stream">
      <div class="adivider"><span class="adivider-text">Sniffing</span></div>
      <div class="arow16">
        <div class="acol24 xin-checks">
          <label class="acheckbox"><input v-model="x.sniffing.enabled" type="checkbox" /><span>{{ L('table.enabled', 'Enabled') }}</span></label>
          <template v-if="x.sniffing.enabled">
            <label v-for="d in ['http', 'tls', 'quic', 'fakedns']" :key="d" class="acheckbox"><input type="checkbox" :checked="x.sniffing.destOverride.includes(d)" @change="toggle(x.sniffing.destOverride, d)" /><span>{{ d }}</span></label>
            <label class="acheckbox"><input v-model="x.sniffing.metadataOnly" type="checkbox" /><span>Metadata only</span></label>
            <label class="acheckbox"><input v-model="x.sniffing.routeOnly" type="checkbox" /><span>Route only</span></label>
          </template>
        </div>
      </div>
    </template>

    <!-- Pasarguard writes its users' links from a host. -->
    <template v-if="isPG">
      <div class="adivider"><span class="adivider-text">{{ L('xin.host', 'Host (for the links)') }}</span></div>
      <div class="arow16">
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-ha">Address <HelpTip :text="L('xin.hostHint', 'What customers connect to; empty is the node’s own address.')" /></label><label class="ainput block"><input id="xin-ha" v-model="x.host.address" class="ltr" /></label></div></div>
        <div class="acol6"><div class="aform-item"><label class="aform-label" for="xin-hp">Port</label><label class="ainput number"><input id="xin-hp" v-model="x.host.port" type="number" min="1" max="65535" class="ltr" :placeholder="String(port)" /></label></div></div>
        <div class="acol6"><div class="aform-item"><label class="aform-label" for="xin-hf">Fingerprint</label>
          <div class="aselect"><select id="xin-hf" v-model="x.host.fingerprint"><option value="">{{ L('xin.default', 'default') }}</option><option v-for="f in FINGERPRINTS" :key="f" :value="f">{{ f }}</option></select></div></div></div>
        <div class="acol8"><div class="aform-item"><label class="aform-label" for="xin-hs">SNI</label><label class="ainput block"><input id="xin-hs" v-model="x.host.sni" class="ltr" /></label></div></div>
        <div class="acol8"><div class="aform-item"><label class="aform-label" for="xin-hh">Host</label><label class="ainput block"><input id="xin-hh" v-model="x.host.host" class="ltr" /></label></div></div>
        <div class="acol8"><div class="aform-item"><label class="aform-label" for="xin-hpa">Path</label><label class="ainput block"><input id="xin-hpa" v-model="x.host.path" class="ltr" /></label></div></div>
      </div>
    </template>

    <!-- Anything else that panel's xray takes, laid over what is above. -->
    <details class="xin-adv">
      <summary>{{ L('xin.advanced', 'Advanced JSON') }}</summary>
      <div class="arow16">
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-as">settings</label>
          <textarea id="xin-as" v-model="x.advSettings" class="atextarea json ltr mono" rows="5" spellcheck="false" placeholder="{}"></textarea></div></div>
        <div class="acol12"><div class="aform-item"><label class="aform-label" for="xin-ast">streamSettings</label>
          <textarea id="xin-ast" v-model="x.advStream" class="atextarea json ltr mono" rows="5" spellcheck="false" placeholder="{}"></textarea></div></div>
      </div>
      <p v-if="advError" class="field-error" role="alert">{{ advError }}</p>
    </details>
  </div>
</template>

<style scoped>
.xin-version { margin: 0 0 8px; }
.xin-keyrow { display: flex; gap: 8px; align-items: center; }
.xin-keyrow .ainput { flex: 1; }
.xin-check { margin-top: 30px; }
.xin-checks { display: flex; flex-wrap: wrap; gap: 8px 18px; margin-bottom: 12px; }
.xin-chips { display: flex; flex-wrap: wrap; gap: 8px 18px; }
.xin-sub { display: flex; align-items: center; gap: 8px; margin: 4px 0 8px; font-weight: 500; }
.xin-fallback { margin-bottom: 6px; }
.xin-adv { margin-top: 8px; }
.xin-adv summary { cursor: pointer; color: var(--muted); margin-bottom: 8px; }
.mono { font-family: var(--mono, ui-monospace, monospace); }
</style>
