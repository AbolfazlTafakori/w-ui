<script setup>
// API tokens for an admin's or a reseller's own scripts. A token reaches what
// they do as they are at each request -- never their account, the operators,
// the settings or another token -- so this page is where one is made, read
// once, and revoked. The owner issues machine tokens elsewhere, and here sees
// and revokes every operator's; an admin sees their resellers' too.
import { computed, onMounted, ref } from 'vue'
import { api } from '../lib/api.js'
import { relative } from '../lib/format.js'
import { store, t, notify } from '../lib/store.js'
import { copyText as toClipboard } from '../lib/clipboard.js'
import AntIcon from '../components/AntIcon.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'

const tokens = ref([])
const loading = ref(true)
const createOpen = ref(false)
const createName = ref('')
const createDays = ref(90)
const creating = ref(false)
const created = ref(null)
const revoking = ref(null)

const owner = computed(() => !!store.admin?.managesPanel)
const me = computed(() => store.admin?.id)
// Who issues here: an admin, out of the tokens the owner gave them, for
// themselves or one of their resellers. A reseller's are issued by their
// admin, so a reseller only reads and revokes their own; the owner issues
// from the operators page.
const issues = computed(() => store.admin?.role === 'admin')
const left = computed(() => {
  const tk = store.admin?.tokenCapacity
  return tk ? Math.max((Number(tk.granted) || 0) - (Number(tk.used) || 0), 0) : 0
})
// The admin's resellers, to issue a token for one of them.
const resellers = ref([])
const createFor = ref(0)

async function load() {
  try {
    const res = await api.get('/api/operator/tokens')
    tokens.value = res.items || []
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    loading.value = false
  }
}
onMounted(async () => {
  await load()
  if (issues.value) {
    try {
      resellers.value = ((await api.admins()).items || []).filter((a) => a.role === 'reseller')
    } catch {
      resellers.value = []
    }
  }
})

// The admin's own count moved: read it again so the button and the card
// say what is left.
async function refreshMe() {
  try {
    const me = await api.get('/api/auth/me')
    if (me && store.admin) store.admin.tokenCapacity = me.tokenCapacity
  } catch {
    // The list itself is right; the count catches up on the next page.
  }
}

async function create() {
  if (!createName.value.trim()) return
  creating.value = true
  try {
    const body = { name: createName.value.trim(), days: Number(createDays.value) }
    if (createFor.value) body.forAdminId = Number(createFor.value)
    created.value = await api.post('/api/operator/tokens', body)
    createOpen.value = false
    createName.value = ''
    createFor.value = 0
    await Promise.all([load(), refreshMe()])
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    creating.value = false
  }
}

async function revoke() {
  const tk = revoking.value
  revoking.value = null
  try {
    await api.del(`/api/operator/tokens/${tk.id}`)
    notify(t('settings.tokenRevoked', { n: tk.name }), 'success')
    await load()
  } catch (e) {
    notify(e.message, 'error')
  }
}

async function copy(text) {
  try {
    await toClipboard(text)
    notify(t('action.copied'), 'success')
  } catch {
    notify(t('share.copyFailed'), 'error')
  }
}

const expired = (tk) => !!tk.expiresAt && new Date(tk.expiresAt) <= new Date()
function ends(tk) {
  if (!tk.expiresAt) return t('opTokens.noEnd')
  if (expired(tk)) return t('opTokens.expired')
  return t('opTokens.ends', { d: new Date(tk.expiresAt).toLocaleDateString(store.locale) })
}
</script>

<template>
  <div class="antpage tokens">
    <div class="acard small">
      <div class="acard-body">
        <div class="api-token-header">
          <p class="api-token-hint">{{ owner ? t('opTokens.ownerHint') : t('opTokens.hint') }}</p>
          <span v-if="issues" class="api-token-left">{{ t('cap.tokensLeft', { n: left }) }}</span>
          <button v-if="issues" class="abtn primary sm" :disabled="left <= 0" :title="left <= 0 ? t('cap.err.tokens') : ''" @click="createOpen = true">
            + {{ t('set.security.apiTokenNew') }}
          </button>
        </div>
        <div v-if="!tokens.length && !loading" class="aempty"><AntIcon name="InboxOutlined" />{{ t('opTokens.empty') }}</div>
        <div v-for="tk in tokens" :key="tk.id" class="api-token-row" :class="{ disabled: expired(tk) }">
          <div class="api-token-row-head">
            <div class="api-token-name-wrap">
              <span class="api-token-name">{{ tk.name }}<span v-if="tk.adminId !== me" class="atag" style="margin-inline-start: 6px">{{ tk.username }}</span></span>
              <span class="api-token-created">
                <code class="ltr">{{ tk.prefix }}…</code> · {{ ends(tk) }} ·
                {{ tk.lastUsedAt ? relative(tk.lastUsedAt, store.locale) : t('settings.tokenNeverUsed') }}
              </span>
            </div>
            <div class="api-token-actions">
              <button class="abtn text sm" @click="revoking = tk">{{ t('set.delete') }}</button>
            </div>
          </div>
        </div>
      </div>
    </div>

    <ConfirmDialog
      :open="!!revoking"
      :title="t('settings.revokeTitle')"
      :body="t('opTokens.revokeBody', { n: revoking?.name || '' })"
      :confirm-label="t('set.delete')"
      :danger="true"
      @confirm="revoke"
      @cancel="revoking = null"
    />

    <div v-if="createOpen" class="amodal-backdrop" @click.self="createOpen = false">
      <div class="amodal" role="dialog" aria-modal="true">
        <div class="amodal-head">
          <h2 class="amodal-title">{{ t('set.security.apiTokenNew') }}</h2>
          <button type="button" class="amodal-close" :aria-label="t('common.close')" @click="createOpen = false"><AntIcon name="CloseOutlined" /></button>
        </div>
        <div class="amodal-body">
          <div class="aform-item">
            <label class="aform-label required" for="op-tk-name">{{ t('set.security.apiTokenName') }}</label>
            <label class="ainput block"><input id="op-tk-name" v-model="createName" maxlength="64" :placeholder="t('opTokens.namePlaceholder')" @keydown.enter="create" /></label>
          </div>
          <div v-if="resellers.length" class="aform-item">
            <label class="aform-label" for="op-tk-for">{{ t('cap.tokenFor') }}</label>
            <div class="aselect"><select id="op-tk-for" v-model="createFor">
              <option :value="0">{{ t('cap.tokenForMe') }}</option>
              <option v-for="r in resellers" :key="r.id" :value="r.id">{{ r.username }}</option>
            </select></div>
          </div>
          <div class="aform-item">
            <label class="aform-label" for="op-tk-days">{{ t('opTokens.lasts') }}</label>
            <div class="aselect"><select id="op-tk-days" v-model="createDays">
              <option :value="30">{{ t('opTokens.days', { n: 30 }) }}</option>
              <option :value="90">{{ t('opTokens.days', { n: 90 }) }}</option>
              <option :value="365">{{ t('opTokens.days', { n: 365 }) }}</option>
              <option :value="0">{{ t('opTokens.noEnd') }}</option>
            </select></div>
          </div>
        </div>
        <div class="amodal-foot">
          <button class="abtn" @click="createOpen = false">{{ t('set.cancel') }}</button>
          <span class="api-token-cost">{{ t('cap.tokenCost') }}</span>
          <button class="abtn primary" :disabled="creating || !createName.trim()" @click="create"><span v-if="creating" class="spin sm"></span>{{ t('set.confirm') }}</button>
        </div>
      </div>
    </div>

    <!-- The token, once: it is kept nowhere in readable form. -->
    <div v-if="created" class="amodal-backdrop" @click.self="created = null">
      <div class="amodal" role="dialog" aria-modal="true">
        <div class="amodal-head">
          <h2 class="amodal-title">{{ t('set.security.apiTokenCreatedTitle') }}</h2>
          <button type="button" class="amodal-close" :aria-label="t('common.close')" @click="created = null"><AntIcon name="CloseOutlined" /></button>
        </div>
        <div class="amodal-body">
          <p class="api-token-created-notice">{{ t('set.security.apiTokenCreatedNotice') }}</p>
          <div class="api-token-value-wrap">
            <code class="api-token-value ltr">{{ created.token }}</code>
            <button class="abtn primary sm" @click="copy(created.token)">{{ t('set.copy') }}</button>
          </div>
        </div>
        <div class="amodal-foot"><button class="abtn primary" @click="created = null">{{ t('set.done') }}</button></div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.api-token-header { display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap; margin-bottom: 10px; }
.api-token-hint { margin: 0; font-size: 13px; opacity: 0.75; flex: 1; min-width: 200px; }
.api-token-row { border: 1px solid var(--line-soft); border-radius: 8px; padding: 10px 12px; margin-bottom: 8px; }
.api-token-row.disabled { opacity: 0.55; }
.api-token-row-head { display: flex; align-items: center; justify-content: space-between; gap: 8px; flex-wrap: wrap; }
.api-token-name-wrap { display: flex; flex-direction: column; gap: 2px; }
.api-token-name { font-weight: 600; font-size: 13.5px; }
.api-token-created { font-size: 11px; opacity: 0.6; }
.api-token-actions { display: flex; align-items: center; gap: 8px; }
.api-token-created-notice { margin-top: 0; }
.api-token-value-wrap { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
.api-token-value { flex: 1; min-width: 0; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12.5px; padding: 4px 8px; background: var(--surface-3); border-radius: 4px; word-break: break-all; }
.api-token-left { color: var(--muted); font-size: 13px; margin-inline-start: auto; margin-inline-end: 8px; }
.api-token-cost { margin-inline-end: auto; align-self: center; color: var(--muted); font-size: 13px; }
</style>
