<script setup>
// Adding or editing a node: the same 760px horizontal form the other dialogs
// use, labels at the end of a third of the row and the control beside them,
// in three groups -- the server, how it is trusted, what it costs.
import { ref, watch } from 'vue'
import { api } from '../../lib/api.js'
import { t, notify } from '../../lib/store.js'
import { nodeBody, nodeForm } from '../../lib/nodes/rows.js'
import AntIcon from '../AntIcon.vue'

const props = defineProps({
  // The node being edited; none to add one.
  node: { type: Object, default: null },
})
// saved(made): made is the node just added, or null after an edit.
const emit = defineEmits(['close', 'saved'])

const editing = !!props.node
const form = ref(nodeForm(props.node))
const busy = ref(false)
const pinBusy = ref(false)

// This panel's own authority, which an operator copies once into each node.
//
// Only ever read here: the key behind it stays in this panel, which is the
// whole reason a certificate is worth more than a token that travels.
const authority = ref('')
const authorityBusy = ref(false)

async function loadAuthority() {
  authorityBusy.value = true
  try {
    const res = await api.get('/api/nodes/mtls/authority')
    authority.value = res?.caCert || ''
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    authorityBusy.value = false
  }
}

async function copyAuthority() {
  try {
    await navigator.clipboard.writeText(authority.value)
    notify(t('node.authorityCopied'), 'success')
  } catch {
    notify(t('action.copyFailed'), 'error')
  }
}

// Fetched as soon as the mode is chosen, because the value is the point of
// choosing it and a button nobody presses leaves an empty box.
watch(
  () => form.value.tlsMode,
  (mode) => {
    if (mode === 'mtls' && !authority.value) loadAuthority()
  },
  { immediate: true },
)

// Read the fingerprint the address is presenting, so nobody has to run openssl
// and copy a hash by hand — which is how certificate checking ends up switched
// off instead. Nothing is verified while reading it, and the hint under the
// field says so.
async function fetchPin() {
  const address = (form.value.address || '').trim()
  if (!address) {
    notify(t('node.addressFirst'), 'error')
    return
  }
  pinBusy.value = true
  try {
    const res = await api.post('/api/nodes/fetch-pin', {
      address,
      allowPrivateAddress: !!form.value.allowPrivateAddress,
      tlsServerName: (form.value.tlsServerName || '').trim(),
    })
    form.value.tlsPin = res?.tlsPin || ''
    notify(t('node.pinFetched'), 'success')
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    pinBusy.value = false
  }
}

async function submit() {
  busy.value = true
  try {
    const body = nodeBody(form.value)
    const made = editing ? null : await api.post('/api/nodes', body)
    if (editing) await api.patch(`/api/nodes/${props.node.id}`, body)
    emit('saved', made)
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="amodal-backdrop" @click.self="$emit('close')">
    <div class="amodal w760" role="dialog" aria-modal="true" aria-labelledby="n-title">
      <div class="amodal-head">
        <h2 id="n-title" class="amodal-title">{{ editing ? t('node.edit') : t('node.add') }}</h2>
        <button class="amodal-close" :aria-label="t('action.cancel')" @click="$emit('close')"><AntIcon name="CloseOutlined" /></button>
      </div>

      <div class="amodal-body nf-body">
        <form id="n-form" class="hform" @submit.prevent="submit">
          <div class="nf-section">{{ t('node.sectionServer') }}</div>
          <div class="hrow">
            <label for="n-kind">{{ t('node.kind') }}</label>
            <div class="hctl">
              <label class="aselect"><select id="n-kind" v-model="form.kind" :disabled="editing">
                <option value="remote">{{ t('node.kindWui') }}</option>
                <option value="xui">{{ t('node.kindXui') }}</option>
                <option value="pasarguard">{{ t('node.kindPg') }}</option>
              </select></label>
              <p v-if="form.kind === 'xui'" class="hint">{{ t('node.kindXuiHint') }}</p>
              <p v-else-if="form.kind === 'pasarguard'" class="hint">{{ t('node.kindPgHint') }}</p>
            </div>
          </div>
          <div class="hrow">
            <label class="req" for="n-name">{{ t('node.name') }}</label>
            <div class="hctl"><label class="ainput block"><input id="n-name" v-model="form.name" required autofocus maxlength="64" /></label></div>
          </div>
          <div class="hrow">
            <label class="req" for="n-addr">{{ t('node.address') }}</label>
            <div class="hctl">
              <label class="ainput block"><input id="n-addr" v-model="form.address" required :placeholder="form.kind === 'xui' ? 'https://vpn2.example.com:2053/secretpath' : form.kind === 'pasarguard' ? 'https://vpn2.example.com:8000' : 'https://vpn2.example.com:2096'" class="ltr" /></label>
              <p class="hint">{{ form.kind === 'xui' ? t('node.addressHintXui') : form.kind === 'pasarguard' ? t('node.addressHintPg') : t('node.addressHint') }}</p>
              <label class="acheckbox nf-check"><input v-model="form.allowPrivateAddress" type="checkbox" /><span>{{ t('node.allowPrivate') }}</span></label>
              <p v-if="form.allowPrivateAddress" class="hint">{{ t('node.allowPrivateHint') }}</p>
            </div>
          </div>
          <div class="hrow">
            <label for="n-note">{{ t('group.note') }}</label>
            <div class="hctl"><label class="ainput block"><input id="n-note" v-model="form.note" maxlength="256" /></label></div>
          </div>

          <div class="nf-section">{{ t('node.sectionTrust') }}</div>
          <div class="hrow">
            <label :class="{ req: !editing }" for="n-token">{{ t('node.token') }}</label>
            <div class="hctl">
              <label class="ainput block"><input id="n-token" v-model="form.token" type="password" class="ltr" autocomplete="off"
                     :required="!editing && !(form.kind !== 'remote' && form.username && form.password)" :placeholder="editing ? t('node.tokenKeep') : (form.kind === 'remote' ? 'wui_…' : '')" /></label>
              <p class="hint">{{ form.kind === 'xui' ? t('node.tokenHintXui') : form.kind === 'pasarguard' ? t('node.tokenHintPg') : t('node.tokenHint') }}</p>
            </div>
          </div>
          <template v-if="form.kind !== 'remote'">
            <div class="hrow">
              <label for="n-user">{{ t('node.signInUser') }}</label>
              <div class="hctl">
                <label class="ainput block"><input id="n-user" v-model="form.username" class="ltr" autocomplete="off" /></label>
                <p class="hint">{{ t('node.signInHint') }}</p>
              </div>
            </div>
            <div class="hrow">
              <label for="n-pass">{{ t('node.signInPassword') }}</label>
              <div class="hctl"><label class="ainput block"><input id="n-pass" v-model="form.password" type="password" class="ltr" autocomplete="new-password" :placeholder="editing ? t('node.tokenKeep') : ''" /></label></div>
            </div>
          </template>
          <div class="hrow">
            <label for="n-tls">{{ t('node.tlsMode') }}</label>
            <div class="hctl">
              <label class="aselect"><select id="n-tls" v-model="form.tlsMode">
                <option value="verify">{{ t('node.tlsVerify') }}</option>
                <option value="pin">{{ t('node.tlsPin') }}</option>
                <option v-if="form.kind === 'remote'" value="mtls">{{ t('node.tlsMutual') }}</option>
                <option value="skip">{{ t('node.tlsSkip') }}</option>
              </select></label>
              <p v-if="form.tlsMode === 'verify'" class="hint">{{ t('node.tlsModeHint') }}</p>
              <p v-else-if="form.tlsMode === 'pin'" class="hint">{{ t('node.tlsPinnedHint') }}</p>
              <p v-else-if="form.tlsMode === 'mtls'" class="hint">{{ t('node.tlsMutualHint') }}</p>
              <p v-else class="hint warn-text">{{ t('node.tlsSkipWarning') }}</p>
            </div>
          </div>
          <div v-if="form.tlsMode === 'pin'" class="hrow">
            <label for="n-pin">{{ t('node.pin') }}</label>
            <div class="hctl">
              <div class="nf-inline">
                <label class="ainput block"><input id="n-pin" v-model="form.tlsPin" class="ltr mono" placeholder="sha256/…" /></label>
                <button type="button" class="abtn" :disabled="pinBusy || !form.address" @click="fetchPin">
                  <span v-if="pinBusy" class="spin sm"></span>
                  <template v-else><AntIcon name="DownloadOutlined" /><span>{{ t('node.fetchPin') }}</span></template>
                </button>
              </div>
              <p class="hint">{{ t('node.fetchPinHint') }}</p>
            </div>
          </div>
          <div class="hrow">
            <label for="n-sni">{{ t('node.tlsServerName') }}</label>
            <div class="hctl">
              <label class="ainput block"><input id="n-sni" v-model="form.tlsServerName" class="ltr mono" placeholder="www.example.com" /></label>
              <p class="hint">{{ t('node.tlsServerNameHint') }}</p>
            </div>
          </div>
          <div v-if="form.tlsMode === 'mtls'" class="hrow">
            <label>{{ t('node.authority') }}</label>
            <div class="hctl">
              <textarea v-model="authority" rows="4" readonly class="atextarea ltr mono" :placeholder="t('node.authorityLoading')"></textarea>
              <div class="nf-inline nf-actions">
                <button type="button" class="abtn" :disabled="authorityBusy" @click="loadAuthority">
                  <span v-if="authorityBusy" class="spin sm"></span>
                  <template v-else><AntIcon name="SafetyCertificateOutlined" /><span>{{ t('node.authorityLoad') }}</span></template>
                </button>
                <button type="button" class="abtn" :disabled="!authority" @click="copyAuthority"><AntIcon name="CopyOutlined" /><span>{{ t('action.copy') }}</span></button>
              </div>
              <p class="hint">{{ t('node.authorityHint') }}</p>
            </div>
          </div>

          <div class="nf-section">{{ t('node.sectionBilling') }}</div>
          <div class="hrow">
            <label for="n-coef">{{ t('node.coefficient') }}</label>
            <div class="hctl">
              <label class="ainput number" style="width: 140px"><input id="n-coef" v-model="form.usageCoefficient" type="number" step="0.1" min="0.1" max="100" class="ltr" /><span class="ainput-suffix">×</span></label>
              <p class="hint">{{ t('node.coefficientHint') }}</p>
            </div>
          </div>
          <div class="hrow">
            <label for="n-limit">{{ t('node.dataLimit') }}</label>
            <div class="hctl">
              <div class="nf-inline">
                <label class="ainput number" style="width: 140px"><input id="n-limit" v-model="form.dataLimitGB" type="number" min="0" step="1" placeholder="∞" class="ltr" /><span class="ainput-suffix">GB</span></label>
                <span class="nf-sep">{{ t('node.resetDay') }}</span>
                <label class="ainput number" style="width: 110px"><input id="n-resetday" v-model="form.resetDay" type="number" min="0" max="28" step="1" placeholder="0" class="ltr" /></label>
              </div>
              <p class="hint">{{ t('node.dataLimitHint') }}</p>
            </div>
          </div>
        </form>
      </div>

      <div class="amodal-foot">
        <button type="button" class="abtn" @click="$emit('close')">{{ t('action.cancel') }}</button>
        <button type="submit" form="n-form" class="abtn primary" :disabled="busy">
          <span v-if="busy" class="spin sm"></span>
          <template v-else>{{ t('action.save') }}</template>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.nf-body { max-height: 72vh; overflow-y: auto; overflow-x: hidden; }
.hint { margin: 4px 0 0; font-size: 12px; color: var(--faint); line-height: 1.5; }
.hint.warn-text { color: var(--warn); }
/* A group heading: small caps over a rule, the way a settings page breaks
   its rows up, so a nine-row form reads as three short ones. */
.nf-section {
  margin: 4px 0 16px;
  padding-bottom: 6px;
  border-bottom: 1px solid var(--line-soft);
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.4px;
  text-transform: uppercase;
  color: var(--muted);
}
.nf-section:not(:first-child) { margin-top: 8px; }
.nf-check { display: inline-flex; margin-top: 8px; }
.nf-inline { display: flex; align-items: center; gap: 8px; }
.nf-inline .ainput.block { flex: 1; }
.nf-actions { margin-top: 8px; }
.nf-sep { color: var(--muted); font-size: 13px; white-space: nowrap; }
.ainput-suffix { color: var(--faint); font-size: 12px; padding-inline-end: 10px; }
.atextarea {
  width: 100%;
  min-height: 96px;
  padding: 6px 11px;
  border: 1px solid var(--line);
  border-radius: 6px;
  background: var(--surface);
  color: var(--ink);
  font-size: 12px;
  line-height: 1.5;
  resize: vertical;
}
.atextarea:focus { outline: none; border-color: var(--accent-hover); box-shadow: 0 0 0 2px var(--accent-ring); }
@media (max-width: 640px) {
  .hrow { grid-template-columns: 1fr; }
  .hrow > label { text-align: start; padding-top: 0; }
}
</style>
