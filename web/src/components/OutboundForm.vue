<script setup>
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '../lib/api.js'
import { t, notify } from '../lib/store.js'
import Icon from './Icon.vue'

// The outbound form, laid out the way 3x-ui's is: a 780px dialog titled
// "+ Outbounds", two tabs -- Basics and JSON -- and a horizontal form with
// the label in the left third and the control beside it. Close and Create
// (Save Changes on edit) in the footer.
//
// What each of their fields does, and what became of it here:
//
//   Protocol / Tag         -- the same. Only the kinds this panel can carry.
//   Address                -- for a WireGuard hop, the addresses the upstream
//                             issued this server, comma-separated, one per
//                             family; put on the hop device.
//   Private Key + reload   -- the hop's own key. Regenerate makes a fresh pair;
//                             the Public Key beside it is derived from the
//                             private one and is what the upstream is told.
//   MTU                    -- the hop device's MTU.
//   Peers                  -- Endpoint, Public Key, PSK, Allowed IPs, Keep
//                             alive, written into the hop's [Peer] section.
//                             A kernel hop here carries one peer.
//   Address + Port         -- for a proxy, where it listens; Username and
//                             Password for one that wants them.
//
// Their Send Through, Target Strategy, Domain Strategy, No-kernel TUN,
// Reserved, Transmission, Security, Sockopts, TCP Masks and Mux are Xray's
// userspace dialer being configured -- which local address it binds, how it
// resolves names, which transport it wraps the connection in. A kernel
// WireGuard hop has none of those knobs: the kernel routes into the device,
// and the device speaks WireGuard. They are left out rather than shown as
// controls that would change nothing.

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

const PROTOCOLS = ['wireguard', 'socks', 'http']

function blank() {
  return {
    tag: '',
    kind: 'wireguard',
    address: '',
    port: 443,
    note: '',
    username: '',
    password: '',
    privateKey: '',
    publicKey: '',
    peerPubKey: '',
    presharedKey: '',
    hopAddress: '',
    hopDns: '',
    hopMtu: 1380,
    allowedIps: ['0.0.0.0/0', '::/0'],
    keepalive: 25,
  }
}
const form = ref(blank())

const isWireGuard = computed(() => form.value.kind === 'wireguard')
const isProxy = computed(() => form.value.kind === 'socks' || form.value.kind === 'http')

// The proxy's host and port are two fields, as theirs are, but one address
// underneath. A WireGuard endpoint stays one field: host:port is how every
// WireGuard config writes it.
function splitHostPort(addr) {
  const m = /^(.*):(\d+)$/.exec(addr || '')
  return m ? { host: m[1].replace(/^\[|\]$/g, ''), port: Number(m[2]) } : { host: addr || '', port: 443 }
}
function joinHostPort(host, port) {
  host = (host || '').trim()
  if (!host) return ''
  const h = host.includes(':') && !host.startsWith('[') ? `[${host}]` : host
  return `${h}:${port || 443}`
}

onMounted(() => {
  if (!props.outbound) return
  const o = props.outbound
  const hp = splitHostPort(o.address)
  // Secrets are never sent back to the browser, so those fields start empty
  // and an empty one on save means "keep what is stored".
  Object.assign(form.value, {
    tag: o.tag,
    kind: o.kind,
    address: o.kind === 'wireguard' ? o.address || '' : hp.host,
    port: hp.port,
    note: o.note || '',
    username: o.username || '',
    peerPubKey: o.peerPubKey || '',
    hopAddress: o.hopAddress || '',
    hopDns: o.hopDns || '',
    hopMtu: o.hopMtu || 1380,
    allowedIps: (o.allowedIps || '').split(',').map((s) => s.trim()).filter(Boolean),
    keepalive: o.keepalive || 25,
  })
  if (!form.value.allowedIps.length) form.value.allowedIps = ['0.0.0.0/0', '::/0']
})

watch(
  () => ({ ...form.value }),
  () => {
    fieldError.value = {}
    formError.value = ''
  },
  { deep: true },
)

// The tag warning theirs shows while typing, before the server says no.
const duplicateTag = computed(() => {
  const mine = form.value.tag.trim()
  if (!mine) return false
  if (editing.value && props.outbound.tag === mine) return false
  return props.existingTags.includes(mine)
})

// ── keys ──
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
// The public key follows the private one, as theirs does, so a key pasted
// in shows what the upstream has to be told.
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

// ── allowed IPs list ──
function addAllowed() {
  form.value.allowedIps.push('')
}
function removeAllowed(i) {
  form.value.allowedIps.splice(i, 1)
}

// ── payload ──
function payload() {
  const f = form.value
  const body = {
    tag: f.tag.trim(),
    kind: f.kind,
    note: f.note,
  }
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
  } else {
    Object.assign(body, {
      address: joinHostPort(f.address, f.port),
      username: f.username,
      password: f.password,
    })
  }
  return body
}

// ── JSON tab ──
// The same object the API takes, shown and editable. Leaving the tab with a
// change applies it to the form; a link pasted above it fills the form in.
const jsonText = ref('')
const jsonDirty = ref(false)
const linkInput = ref('')

function toJSON() {
  jsonText.value = JSON.stringify(payload(), null, 2)
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
  fromObject(parsed)
  jsonDirty.value = false
  return true
}
function fromObject(o) {
  const f = form.value
  if (o.kind && PROTOCOLS.includes(o.kind)) f.kind = o.kind
  if (typeof o.tag === 'string') f.tag = o.tag
  if (typeof o.note === 'string') f.note = o.note
  if (f.kind === 'wireguard') {
    if (typeof o.address === 'string') f.address = o.address
    if (typeof o.privateKey === 'string') f.privateKey = o.privateKey
    if (typeof o.peerPubKey === 'string') f.peerPubKey = o.peerPubKey
    if (typeof o.presharedKey === 'string') f.presharedKey = o.presharedKey
    if (typeof o.hopAddress === 'string') f.hopAddress = o.hopAddress
    if (typeof o.hopDns === 'string') f.hopDns = o.hopDns
    if (o.hopMtu != null) f.hopMtu = Number(o.hopMtu) || 1380
    if (typeof o.allowedIps === 'string') {
      f.allowedIps = o.allowedIps.split(',').map((s) => s.trim()).filter(Boolean)
    } else if (Array.isArray(o.allowedIps)) f.allowedIps = o.allowedIps.map(String)
    if (o.keepalive != null) f.keepalive = Number(o.keepalive) || 0
  } else {
    if (typeof o.address === 'string') {
      const hp = splitHostPort(o.address)
      f.address = hp.host
      f.port = hp.port
    }
    if (typeof o.username === 'string') f.username = o.username
    if (typeof o.password === 'string') f.password = o.password
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

// A share link, the way theirs takes one: wireguard://, socks://, http://.
// wireguard://<privateKey>@host:port?publickey=&presharedkey=&address=&mtu=&keepalive=#tag
function importLink() {
  const raw = linkInput.value.trim()
  if (!raw) return
  let u
  try {
    u = new URL(raw)
  } catch {
    notify(t('outbound.form.wrongLink'), 'error')
    return
  }
  const scheme = u.protocol.replace(':', '').toLowerCase()
  const tag = decodeURIComponent(u.hash.replace(/^#/, '')) || form.value.tag
  const q = u.searchParams
  let next
  if (scheme === 'wireguard' || scheme === 'wg') {
    next = {
      kind: 'wireguard',
      tag,
      address: u.host,
      privateKey: decodeURIComponent(u.username || ''),
      peerPubKey: q.get('publickey') || q.get('pubkey') || '',
      presharedKey: q.get('presharedkey') || q.get('psk') || '',
      hopAddress: q.get('address') || '',
      hopMtu: Number(q.get('mtu')) || 1380,
      keepalive: Number(q.get('keepalive')) || 25,
      allowedIps: q.get('allowedips') || '0.0.0.0/0, ::/0',
    }
  } else if (['socks', 'socks5', 'socks5h', 'http', 'https'].includes(scheme)) {
    next = {
      kind: scheme.startsWith('socks') ? 'socks' : 'http',
      tag,
      address: u.host,
      username: decodeURIComponent(u.username || ''),
      password: decodeURIComponent(u.password || ''),
    }
  } else {
    notify(t('outbound.form.wrongLink'), 'error')
    return
  }
  fromObject(next)
  linkInput.value = ''
  jsonDirty.value = false
  notify(t('outbound.form.linkImported'), 'success')
  tab.value = 'basics'
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
          <!-- Protocol -->
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

          <!-- Tag -->
          <div class="hrow">
            <label for="ob-tag" class="req">{{ t('outbound.tag') }}</label>
            <div class="hctl">
              <input id="ob-tag" v-model="form.tag" class="ltr" :class="{ warn: duplicateTag }" :disabled="outbound?.builtin" :placeholder="t('outbound.form.tagPlaceholder')" autocomplete="off" />
              <p v-if="fieldError.tag" class="field-error">{{ fieldError.tag }}</p>
              <p v-else-if="duplicateTag" class="field-warn">{{ t('outbound.form.tagDuplicate') }}</p>
            </div>
          </div>

          <!-- Proxy: address + port, then credentials -->
          <template v-if="isProxy">
            <div class="hrow">
              <label for="ob-address" class="req">{{ t('outbound.address') }}</label>
              <div class="hctl">
                <input id="ob-address" v-model="form.address" class="ltr" autocomplete="off" spellcheck="false" />
                <p v-if="fieldError.address" class="field-error">{{ fieldError.address }}</p>
              </div>
            </div>
            <div class="hrow">
              <label for="ob-port" class="req">{{ t('outbound.form.port') }}</label>
              <div class="hctl">
                <input id="ob-port" v-model.number="form.port" class="ltr" type="number" min="1" max="65535" />
              </div>
            </div>
            <div class="hrow">
              <label for="ob-user">{{ t('outbound.username') }}</label>
              <div class="hctl">
                <input id="ob-user" v-model="form.username" class="ltr" autocomplete="off" />
                <p v-if="fieldError.username" class="field-error">{{ fieldError.username }}</p>
              </div>
            </div>
            <div class="hrow">
              <label for="ob-pass">{{ t('outbound.password') }}</label>
              <div class="hctl">
                <input id="ob-pass" v-model="form.password" class="ltr" type="password" autocomplete="new-password" :placeholder="editing ? t('outbound.form.keepSecret') : ''" />
              </div>
            </div>
          </template>

          <!-- WireGuard: their wireguard block, field for field -->
          <template v-if="isWireGuard">
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
              <div class="hctl">
                <input id="ob-pub" :value="form.publicKey" class="ltr" disabled />
              </div>
            </div>
            <div class="hrow">
              <label for="ob-mtu">MTU</label>
              <div class="hctl">
                <input id="ob-mtu" v-model.number="form.hopMtu" class="ltr num-short" type="number" min="0" />
                <p v-if="fieldError.hopMtu" class="field-error">{{ fieldError.hopMtu }}</p>
              </div>
            </div>

            <div class="hrow">
              <label>{{ t('outbound.form.peers') }}</label>
              <div class="hctl">
                <span class="muted small">{{ t('outbound.form.onePeer') }}</span>
              </div>
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
                <button type="button" class="btn sm icon" :aria-label="t('action.add')" @click="addAllowed">
                  <Icon name="plus" :size="13" />
                </button>
                <p v-if="fieldError.allowedIps" class="field-error">{{ fieldError.allowedIps }}</p>
              </div>
            </div>
            <div class="hrow">
              <label for="ob-keepalive">{{ t('outbound.form.keepAlive') }}</label>
              <div class="hctl">
                <input id="ob-keepalive" v-model.number="form.keepalive" class="ltr num-short" type="number" min="0" />
                <p v-if="fieldError.keepalive" class="field-error">{{ fieldError.keepalive }}</p>
              </div>
            </div>
          </template>

          <div class="hrow">
            <label for="ob-note">{{ t('outbound.note') }}</label>
            <div class="hctl">
              <input id="ob-note" v-model="form.note" autocomplete="off" />
            </div>
          </div>
        </form>

        <div v-show="tab === 'json'" class="json-tab">
          <div class="compact">
            <input v-model="linkInput" class="ltr" placeholder="wireguard:// socks:// http://" spellcheck="false" @keydown.enter.prevent="importLink" />
            <button type="button" class="btn primary" @click="importLink">{{ t('outbound.form.import') }}</button>
          </div>
          <textarea v-model="jsonText" class="ltr mono json-editor" rows="18" spellcheck="false" @input="jsonDirty = true"></textarea>
        </div>
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
/* Their Modal width={780}. */
.ob-modal {
  max-width: 780px;
}
.card-body {
  padding: 8px 24px 16px;
}
.tabs {
  margin-bottom: 16px;
}

/* Ant's horizontal Form with labelCol 8 / wrapperCol 14: the label takes a
   third and sits at the end of it, the control the next 14/24ths, and each
   row is 24px apart. */
.hform {
  display: flex;
  flex-direction: column;
}
.hrow {
  display: grid;
  grid-template-columns: 33.333% 58.333%;
  column-gap: 0;
  align-items: start;
  margin-bottom: 24px;
}
.hrow > label {
  padding-inline-end: 8px;
  padding-top: 5px;
  text-align: end;
  font-size: var(--t-base);
  color: var(--ink);
  line-height: 22px;
}
.hrow > label.req::before {
  content: '* ';
  color: var(--bad);
}
.hctl {
  min-width: 0;
}
.hctl > input,
.hctl > select,
.compact > input {
  width: 100%;
}
.num-short {
  max-width: 100%;
}
.compact {
  display: flex;
  align-items: stretch;
}
.compact > input {
  flex: 1;
  min-width: 0;
}
.compact > .btn:not(:first-child),
.compact > input:not(:first-child) {
  margin-inline-start: -1px;
  border-start-start-radius: 0;
  border-end-start-radius: 0;
}
.compact > input:not(:last-child),
.compact > .btn:not(:last-child) {
  border-start-end-radius: 0;
  border-end-end-radius: 0;
}
/* Everything in a row is Ant's controlHeight, 32, the addon included. */
.hctl > select,
.hctl > input,
.compact > input,
.compact > .btn {
  height: 32px;
  min-height: 32px;
}
.btn.addon {
  width: 32px;
  padding: 0;
  flex: none;
}
.list-row {
  margin-bottom: 4px;
}
.item-heading {
  padding: 4px 0;
  font-weight: 600;
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
