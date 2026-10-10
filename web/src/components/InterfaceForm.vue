<script setup>
// A tunnel's form, in the same dress as a customer's: Ant's labels with a
// question mark where there is more to say, rather than a paragraph under
// every field, and the settings only one protocol has gathered under that
// protocol's own heading.
import { computed, nextTick, ref, watch } from 'vue'
import { api } from '../lib/api.js'
import { store, t, notify } from '../lib/store.js'
import AntIcon from './AntIcon.vue'
import HelpTip from './HelpTip.vue'
import Toggle from './Toggle.vue'

const props = defineProps({
  // When present the dialog edits that interface instead of creating one.
  iface: { type: Object, default: null },
  // The servers a tunnel can be placed on. Empty or one means there is nothing
  // to choose, and the field is not shown at all.
  nodes: { type: Array, default: () => [] },
})
const emit = defineEmits(['close', 'submit'])

const editing = computed(() => !!props.iface)

const defaults = {
  wireguard: { name: 'wg0', listenPort: 51820, subnet: '10.66.0.0/16', mtu: 1420 },
  openvpn: { name: 'ovpn0', listenPort: 1194, subnet: '10.8.0.0/16', mtu: 1500 },
  // The port is not a choice: the clients built into the devices use 1701.
  l2tp: { name: 'l2tp0', listenPort: 1701, subnet: '10.77.0.0/16', mtu: 1400 },
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
        transport: props.iface.openvpn?.transport || 'udp',
        psk: props.iface.l2tp?.psk || '',
        l2tpPlain: !!props.iface.l2tp?.allowPlain,
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
        psk: '',
        l2tpPlain: false,
        enabled: true,
      },
)
const busy = ref(false)

// The protocols in the order they are usually chosen, whatever order the
// server lists them in; one it adds later goes at the end.
const ORDER = ['wireguard', 'openvpn', 'l2tp']
const rank = (p) => (ORDER.includes(p) ? ORDER.indexOf(p) : ORDER.length)
const protocols = computed(() => [...(store.meta?.protocols || ['wireguard'])].sort((a, b) => rank(a) - rank(b)))

// Only worth asking when there is more than one answer. A panel that has
// never added a node would otherwise show a select with a single option.
const canChooseNode = computed(() => props.nodes.length > 1)
const isWireGuard = computed(() => form.value.protocol === 'wireguard')
const isOpenVPN = computed(() => form.value.protocol === 'openvpn')
const isL2TP = computed(() => form.value.protocol === 'l2tp')

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
      <div class="amodal-head">
        <h2 id="if-title" class="amodal-title">{{ editing ? t('interface.edit') : t('interface.create') }}</h2>
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

          <div class="arow16">
            <!-- Fixed once the tunnel exists: every configuration handed out
                 speaks this protocol. -->
            <div :class="canChooseNode ? 'acol12' : 'acol24'">
              <div class="aform-item">
                <label class="aform-label required">{{ t('client.protocol') }}</label>
                <div class="aradio-group proto" role="radiogroup" :aria-label="t('client.protocol')">
                  <button
                    v-for="(p, i) in protocols"
                    :key="p"
                    type="button"
                    role="radio"
                    class="aradio-btn"
                    :class="{ active: form.protocol === p }"
                    :aria-checked="form.protocol === p"
                    :disabled="editing && form.protocol !== p"
                    :data-field="i === 0 ? 'protocol' : null"
                    @click="chooseProtocol(p)"
                  >{{ t(`protocol.${p}`) }}</button>
                </div>
                <p v-if="fieldName === 'protocol'" class="field-error" role="alert">{{ fieldError }}</p>
              </div>
            </div>

            <!-- Which server terminates this tunnel. A tunnel on another node is
                 programmed by the panel running there: this one pushes it the
                 interface and the peers. Fixed after creation, because moving a
                 live tunnel between machines would strand every customer on it. -->
            <div v-if="canChooseNode" class="acol12">
              <div class="aform-item">
                <label class="aform-label" for="if-node">{{ t('interface.node') }} <HelpTip v-if="editing" :text="t('interface.nodeLocked')" /></label>
                <div class="aselect" :class="{ disabled: editing, invalid: fieldName === 'nodeId' }">
                  <select id="if-node" v-model="form.nodeId" data-field="nodeId" :disabled="editing" @change="clearError('nodeId')">
                    <option v-for="n in nodes" :key="n.id" :value="n.id">
                      {{ n.name }}{{ n.kind === 'local' ? ` — ${t('interface.thisServer')}` : '' }}
                    </option>
                  </select>
                </div>
                <p v-if="fieldName === 'nodeId'" class="field-error" role="alert">{{ fieldError }}</p>
              </div>
            </div>
          </div>

          <div class="arow16">
            <div class="acol12">
              <div class="aform-item">
                <label class="aform-label required" for="if-name">{{ t('interface.name') }}</label>
                <label class="ainput block" :class="{ invalid: fieldName === 'name' }">
                  <input id="if-name" v-model="form.name" data-field="name" class="ltr" required @input="clearError('name')" />
                </label>
                <p v-if="fieldName === 'name'" class="field-error" role="alert">{{ fieldError }}</p>
              </div>
            </div>
            <div class="acol12">
              <div class="aform-item">
                <label class="aform-label required" for="if-host">{{ t('interface.endpoint') }} <HelpTip :text="t('interface.endpointHint')" /></label>
                <label class="ainput block" :class="{ invalid: fieldName === 'endpointHost' }">
                  <input id="if-host" v-model="form.endpointHost" data-field="endpointHost" class="ltr" placeholder="vpn.example.com" required @input="clearError('endpointHost')" />
                </label>
                <p v-if="fieldName === 'endpointHost'" class="field-error" role="alert">{{ fieldError }}</p>
              </div>
            </div>
          </div>

          <div class="arow16">
            <div class="acol6">
              <div class="aform-item">
                <label class="aform-label required" for="if-port">{{ t('interface.port') }} <HelpTip v-if="isL2TP" :text="t('interface.l2tpPortHint')" /></label>
                <label class="ainput number" :class="{ invalid: fieldName === 'listenPort', disabled: isL2TP }">
                  <input id="if-port" v-model="form.listenPort" data-field="listenPort" type="number" min="1" max="65535" class="ltr" :readonly="isL2TP" required @input="clearError('listenPort')" />
                  <AntIcon v-if="isL2TP" name="LockOutlined" class="ainput-suffix" />
                </label>
                <p v-if="fieldName === 'listenPort'" class="field-error" role="alert">{{ fieldError }}</p>
              </div>
            </div>
            <div class="acol6">
              <div class="aform-item">
                <label class="aform-label" for="if-mtu">MTU</label>
                <label class="ainput number" :class="{ invalid: fieldName === 'mtu' }">
                  <input id="if-mtu" v-model="form.mtu" data-field="mtu" type="number" min="576" max="9000" class="ltr" @input="clearError('mtu')" />
                </label>
                <p v-if="fieldName === 'mtu'" class="field-error" role="alert">{{ fieldError }}</p>
              </div>
            </div>
            <div class="acol12">
              <div class="aform-item">
                <label class="aform-label required" for="if-subnet">{{ t('interface.subnet') }} <HelpTip :text="t('interface.subnetHint')" /></label>
                <label class="ainput block" :class="{ invalid: fieldName === 'subnet' }">
                  <input id="if-subnet" v-model="form.subnet" data-field="subnet" class="ltr" required @input="clearError('subnet')" />
                </label>
                <p v-if="fieldName === 'subnet'" class="field-error" role="alert">{{ fieldError }}</p>
              </div>
            </div>
          </div>

          <div class="arow16">
            <div class="acol12">
              <div class="aform-item">
                <label class="aform-label" for="if-dns">DNS</label>
                <label class="ainput block"><input id="if-dns" v-model="form.dns" class="ltr" placeholder="1.1.1.1" /></label>
              </div>
            </div>
            <div class="acol12">
              <div class="aform-item">
                <label class="aform-label" for="if-nat">{{ t('interface.natInterface') }}</label>
                <label class="ainput block"><input id="if-nat" v-model="form.natInterface" class="ltr" placeholder="eth0" /></label>
              </div>
            </div>
          </div>

          <!-- WireGuard only: AmneziaWG's disguise. -->
          <div v-if="isWireGuard" class="arow16">
            <div class="acol12">
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
          </div>

          <!-- OpenVPN only: WireGuard and L2TP are UDP and have no other
               option, so offering the choice there would be offering a
               setting that does nothing. -->
          <div v-if="isOpenVPN" class="arow16">
            <div class="acol12">
              <div class="aform-item">
                <label class="aform-label" for="if-transport">{{ t('interface.transport') }} <HelpTip :text="form.transport === 'tcp' ? t('interface.transportTcpHint') : t('interface.transportUdpHint')" /></label>
                <div class="aselect" :class="{ invalid: fieldName === 'transport' }">
                  <select id="if-transport" v-model="form.transport" data-field="transport" @change="clearError('transport')">
                    <option value="udp">{{ t('interface.transportUdp') }}</option>
                    <option value="tcp">{{ t('interface.transportTcp') }}</option>
                  </select>
                </div>
                <p v-if="fieldName === 'transport'" class="field-error" role="alert">{{ fieldError }}</p>
              </div>
            </div>
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

          <!-- Port, subnet and mode are baked into every config already handed
               out: changing them is allowed, and said to cost every customer a
               new configuration, so it is done knowingly. -->
          <div v-if="editing" class="aalert info edit-note"><AntIcon name="InfoCircleOutlined" /><span>{{ t('interface.editHint') }}</span></div>
        </form>
      </div>

      <div class="amodal-foot">
        <button class="abtn" type="button" @click="emit('close')">{{ t('action.cancel') }}</button>
        <button class="abtn primary" type="submit" form="iface-form" :disabled="busy">
          <AntIcon v-if="busy" name="LoadingOutlined" class="spin" />
          <template v-else>{{ editing ? t('action.save') : t('action.create') }}</template>
        </button>
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
