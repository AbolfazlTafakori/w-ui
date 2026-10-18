<script setup>
// The settings page's Security tab: the operator's own sign-in, the second
// factor, the API tokens other panels use, and which panel may drive this
// one as a node. None of it is part of the settings form: each saves on its
// own, as it is changed.
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../../lib/api.js'
import { makeQR } from '../../lib/qr.js'
import { relative } from '../../lib/format.js'
import { store, t, notify, signOut } from '../../lib/store.js'
import AntIcon from '../AntIcon.vue'
import Toggle from '../Toggle.vue'
import ConfirmDialog from '../ConfirmDialog.vue'

const router = useRouter()
const inner = ref('1')
const showSecret = ref({})

onMounted(() => {
  loadMe()
  loadTokens()
})

// ── security: credentials ────────────────────────────────────────────────────
const user = ref({ oldUsername: '', oldPassword: '', newUsername: '', newPassword: '' })
const updating = ref(false)
async function updateUser() {
  const u = user.value
  if (!u.oldPassword || (!u.newUsername && !u.newPassword)) {
    notify(t('settings.fillCredentials'), 'error')
    return
  }
  updating.value = true
  try {
    await api.post('/api/auth/password', {
      currentPassword: u.oldPassword,
      newUsername: u.newUsername,
      newPassword: u.newPassword,
    })
    notify(t('set.credentialsChanged'), 'success')
    signOut()
    router.push('/login')
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    updating.value = false
  }
}

// ── security: two-factor, in their modal ─────────────────────────────────────
const twoFactor = ref(false)
const tfa = ref(null) // { type: 'set'|'delete', token, uri, qr, code }
async function loadMe() {
  try {
    const me = await api.get('/api/auth/me')
    twoFactor.value = !!me.twoFactor
  } catch {
    /* the page is still usable without it */
  }
}
async function toggleTwoFactor() {
  if (twoFactor.value) {
    tfa.value = { type: 'delete', code: '' }
    return
  }
  try {
    const res = await api.post('/api/auth/totp/start')
    const qr = await makeQR(res.uri)
    tfa.value = { type: 'set', token: res.secret, uri: res.uri, qr, code: '' }
  } catch (e) {
    notify(e.message, 'error')
  }
}
async function confirmTfa() {
  const m = tfa.value
  try {
    if (m.type === 'set') {
      await api.post('/api/auth/totp/confirm', { secret: m.token, code: m.code })
      twoFactor.value = true
      notify(t('settings.twoFactorEnabled'), 'success')
    } else {
      await api.post('/api/auth/totp/disable', { code: m.code, password: m.password || '' })
      twoFactor.value = false
      notify(t('settings.twoFactorDisabled'), 'success')
    }
    tfa.value = null
  } catch (e) {
    notify(e.message, 'error')
  }
}
async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text)
    notify(t('action.copied'), 'success')
  } catch {
    notify(t('share.copyFailed'), 'error')
  }
}

// ── security: API tokens ─────────────────────────────────────────────────────
const tokens = ref([])
const tokensLoading = ref(false)
const createOpen = ref(false)
const createName = ref('')
const creating = ref(false)
const createdToken = ref(null)
const revoking = ref(null)

async function loadTokens() {
  tokensLoading.value = true
  try {
    tokens.value = await api.get('/api/tokens')
  } catch {
    tokens.value = []
  } finally {
    tokensLoading.value = false
  }
}
async function confirmCreateToken() {
  if (!createName.value.trim()) return
  creating.value = true
  try {
    const res = await api.post('/api/tokens', { name: createName.value.trim() })
    createdToken.value = res
    createOpen.value = false
    createName.value = ''
    await loadTokens()
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    creating.value = false
  }
}
async function toggleTokenEnabled(tk) {
  try {
    await api.patch(`/api/tokens/${tk.id}`, { enabled: !!tk.disabled })
    await loadTokens()
  } catch (e) {
    notify(e.message, 'error')
  }
}
async function doRevoke() {
  const tk = revoking.value
  revoking.value = null
  try {
    await api.del(`/api/tokens/${tk.id}`)
    notify(t('settings.tokenRevoked', { n: tk.name }), 'success')
    await loadTokens()
  } catch (e) {
    notify(e.message, 'error')
  }
}
function tokenDate(iso) {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString(store.locale)
}

// ── mTLS trust for this panel as a node ──────────────────────────────────────
const mtlsTrust = ref('')
const mtlsBusy = ref(false)
async function saveMtlsTrust() {
  mtlsBusy.value = true
  try {
    const res = await api.post('/api/nodes/mtls/trust', { caCert: mtlsTrust.value.trim() })
    notify(res?.required ? t('settings.mtlsTrustSaved') : t('settings.mtlsTrustCleared'), 'ok')
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    mtlsBusy.value = false
  }
}
</script>

<template>
  <div class="security-settings">
    <div class="atabs-nav"><div class="atabs-list">
      <button class="atab" :class="{ active: inner === '1' }" @click="inner = '1'"><AntIcon name="UserOutlined" /><span>{{ t('set.security.admin') }}</span></button>
      <button class="atab" :class="{ active: inner === '2' }" @click="inner = '2'"><AntIcon name="SafetyOutlined" /><span>{{ t('set.security.twoFactor') }}</span></button>
      <button class="atab" :class="{ active: inner === '3' }" @click="inner = '3'"><AntIcon name="ApiOutlined" /><span>{{ t('set.nodes.apiToken') }}</span></button>
    </div></div>

    <template v-if="inner === '1'">
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.oldUsername') }}</div></div></div>
        <div class="acol"><label class="ainput block"><input v-model="user.oldUsername" autocomplete="username" /></label></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.currentPassword') }}</div></div></div>
        <div class="acol"><label class="ainput block"><input v-model="user.oldPassword" :type="showSecret.old ? 'text' : 'password'" autocomplete="current-password" /><button type="button" class="ainput-clear" @click="showSecret.old = !showSecret.old"><AntIcon :name="showSecret.old ? 'EyeOutlined' : 'EyeInvisibleOutlined'" /></button></label></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.newUsername') }}</div></div></div>
        <div class="acol"><label class="ainput block"><input v-model="user.newUsername" /></label></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.newPassword') }}</div></div></div>
        <div class="acol"><label class="ainput block"><input v-model="user.newPassword" :type="showSecret.new ? 'text' : 'password'" autocomplete="new-password" /><button type="button" class="ainput-clear" @click="showSecret.new = !showSecret.new"><AntIcon :name="showSecret.new ? 'EyeOutlined' : 'EyeInvisibleOutlined'" /></button></label></div>
      </div></div>
      <div class="security-actions"><div class="aspace" style="padding: 0 20px">
        <button class="abtn primary" :disabled="updating" @click="updateUser"><span v-if="updating" class="spin sm"></span>{{ t('set.confirm') }}</button>
      </div></div>
    </template>

    <template v-else-if="inner === '2'">
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.security.twoFactorEnable') }}</div><div class="setting-list-description">{{ t('set.security.twoFactorEnableDesc') }}</div></div></div>
        <div class="acol"><Toggle :model-value="twoFactor" :label="t('set.security.twoFactorEnable')" @update:model-value="toggleTwoFactor" /></div>
      </div></div>
      <!-- This panel as a node: which panel may drive it. -->
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('settings.mtlsTrust') }}</div><div class="setting-list-description">{{ t('settings.mtlsTrustHint') }}</div></div></div>
        <div class="acol"><div class="aspace-v">
          <label class="ainput block area"><textarea v-model="mtlsTrust" rows="4" class="ltr mono" placeholder="-----BEGIN CERTIFICATE-----"></textarea></label>
          <div><button class="abtn" :disabled="mtlsBusy" @click="saveMtlsTrust"><span v-if="mtlsBusy" class="spin sm"></span>{{ t('set.save') }}</button></div>
        </div></div>
      </div></div>
    </template>

    <template v-else>
      <div class="api-token-section">
        <div class="api-token-header">
          <p class="api-token-hint">{{ t('set.nodes.apiTokenHint') }}</p>
          <button class="abtn primary sm" @click="createOpen = true">+ {{ t('set.security.apiTokenNew') }}</button>
        </div>
        <div v-if="!tokens.length && !tokensLoading" class="aempty"><AntIcon name="InboxOutlined" />{{ t('set.security.apiTokenEmpty') }}</div>
        <div v-for="tk in tokens" :key="tk.id" class="api-token-row" :class="{ disabled: tk.disabled }">
          <div class="api-token-row-head">
            <div class="api-token-name-wrap">
              <span class="api-token-name">{{ tk.name }}</span>
              <span class="api-token-created">{{ tokenDate(tk.createdAt) }} · <code class="ltr">{{ tk.prefix }}…</code> · {{ tk.lastUsedAt ? relative(tk.lastUsedAt, store.locale) : t('settings.tokenNeverUsed') }}</span>
            </div>
            <div class="api-token-actions">
              <Toggle :model-value="!tk.disabled" small :label="tk.name" @update:model-value="toggleTokenEnabled(tk)" />
              <button class="abtn text sm danger-text" @click="revoking = tk">{{ t('set.delete') }}</button>
            </div>
          </div>
        </div>
      </div>
    </template>

    <ConfirmDialog
      :open="!!revoking"
      :title="t('settings.revokeTitle')"
      :body="t('settings.revokeBody', { n: revoking?.name || '' })"
      :confirm-label="t('set.delete')"
      :danger="true"
      @confirm="doRevoke"
      @cancel="revoking = null"
    />

    <!-- New API token: their Modal with one required Name. -->
    <div v-if="createOpen" class="amodal-backdrop" @click.self="createOpen = false">
      <div class="amodal" role="dialog" aria-modal="true">
        <div class="amodal-head"><h2 class="amodal-title">{{ t('set.security.apiTokenNew') }}</h2><button type="button" class="amodal-close" :aria-label="t('common.close')" @click="createOpen = false"><AntIcon name="CloseOutlined" /></button></div>
        <div class="amodal-body">
          <div class="aform-item"><label class="aform-label required" for="tk-name">{{ t('set.security.apiTokenName') }}</label>
            <label class="ainput block"><input id="tk-name" v-model="createName" maxlength="64" :placeholder="t('set.security.apiTokenNamePlaceholder')" @keydown.enter="confirmCreateToken" /></label></div>
        </div>
        <div class="amodal-foot">
          <button class="abtn" @click="createOpen = false">{{ t('set.cancel') }}</button>
          <button class="abtn primary" :disabled="creating || !createName.trim()" @click="confirmCreateToken"><span v-if="creating" class="spin sm"></span>{{ t('set.confirm') }}</button>
        </div>
      </div>
    </div>

    <!-- The token, once. -->
    <div v-if="createdToken" class="amodal-backdrop" @click.self="createdToken = null">
      <div class="amodal" role="dialog" aria-modal="true">
        <div class="amodal-head"><h2 class="amodal-title">{{ t('set.security.apiTokenCreatedTitle') }}</h2><button type="button" class="amodal-close" :aria-label="t('common.close')" @click="createdToken = null"><AntIcon name="CloseOutlined" /></button></div>
        <div class="amodal-body">
          <p class="api-token-created-notice">{{ t('set.security.apiTokenCreatedNotice') }}</p>
          <div class="api-token-value-wrap">
            <code class="api-token-value ltr">{{ createdToken.token }}</code>
            <button class="abtn primary sm" @click="copyText(createdToken.token)">{{ t('set.copy') }}</button>
          </div>
        </div>
        <div class="amodal-foot"><button class="abtn primary" @click="createdToken = null">{{ t('set.done') }}</button></div>
      </div>
    </div>

    <!-- Two-factor: their modal, steps and QR and code. -->
    <div v-if="tfa" class="amodal-backdrop" @click.self="tfa = null">
      <div class="amodal" role="dialog" aria-modal="true">
        <div class="amodal-head"><h2 class="amodal-title">{{ tfa.type === 'set' ? t('set.twoFactorModalSetTitle') : t('set.twoFactorModalDeleteTitle') }}</h2><button type="button" class="amodal-close" :aria-label="t('common.close')" @click="tfa = null"><AntIcon name="CloseOutlined" /></button></div>
        <div class="amodal-body">
          <template v-if="tfa.type === 'set'">
            <p>{{ t('set.twoFactorModalSteps') }}</p>
            <hr class="adivider" />
            <p>{{ t('set.twoFactorModalFirstStep') }}</p>
            <div class="qr-wrap" role="button" tabindex="0" :aria-label="t('set.copy')" @click="copyText(tfa.token)" @keydown.enter="copyText(tfa.token)">
              <img class="qr-code" :src="tfa.qr.dataUrl" width="180" height="180" alt="" />
              <span class="qr-token ltr">{{ tfa.token }}</span>
            </div>
            <hr class="adivider" />
            <p>{{ t('set.twoFactorModalSecondStep') }}</p>
            <label class="ainput block"><input v-model="tfa.code" inputmode="numeric" maxlength="6" class="ltr" :aria-label="t('set.twoFactorCode')" @keydown.enter="confirmTfa" /></label>
          </template>
          <template v-else>
            <p>{{ t('set.twoFactorModalDeleteDesc') }}</p>
            <label class="ainput block"><input v-model="tfa.code" class="ltr" :placeholder="t('set.twoFactorCode')" :aria-label="t('set.twoFactorCode')" /></label>
            <label class="ainput block" style="margin-top: 8px"><input v-model="tfa.password" type="password" autocomplete="current-password" :placeholder="t('set.password')" /></label>
          </template>
        </div>
        <div class="amodal-foot">
          <button class="abtn" @click="tfa = null">{{ t('set.cancel') }}</button>
          <button class="abtn primary" :disabled="tfa.type === 'set' ? !/^\d{6}$/.test(tfa.code || '') : !(tfa.code || tfa.password)" @click="confirmTfa">{{ t('set.confirm') }}</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.security-actions { padding: 12px 0; display: flex; align-items: center; }
.api-token-section { padding: 8px 20px 16px; display: flex; flex-direction: column; gap: 12px; }
.api-token-header { display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap; }
.api-token-hint { margin: 0; font-size: 12.5px; opacity: 0.7; flex: 1; min-width: 200px; }
.api-token-row { border: 1px solid var(--line-soft); border-radius: 8px; padding: 10px 12px; display: flex; flex-direction: column; gap: 8px; transition: opacity 0.15s; }
.api-token-row.disabled { opacity: 0.55; }
.api-token-row-head { display: flex; align-items: center; justify-content: space-between; gap: 8px; flex-wrap: wrap; }
.api-token-name-wrap { display: flex; flex-direction: column; gap: 2px; }
.api-token-name { font-weight: 600; font-size: 13.5px; }
.api-token-created { font-size: 11px; opacity: 0.55; }
.api-token-actions { display: flex; align-items: center; gap: 8px; }
.api-token-value-wrap { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
.api-token-value { flex: 1; min-width: 0; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12.5px; padding: 4px 8px; background: var(--surface-3); border-radius: 4px; word-break: break-all; }
.api-token-created-notice { margin: 0 0 12px; font-size: 13px; }
.qr-wrap { display: flex; flex-direction: column; align-items: center; gap: 12px; cursor: pointer; }
.qr-code { display: block; border-radius: 4px; background: #fff; }
.qr-token { font-size: 12px; font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; word-break: break-all; text-align: center; }
.adivider { margin: 24px 0; border: 0; border-top: 1px solid var(--line-soft); }
.amodal-body p { margin: 0 0 1em; }
</style>
