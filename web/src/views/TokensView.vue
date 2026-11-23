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

// Embedded on the owner's settings/security page: the list alone, without the
// page's own summary and frame, which the settings card already gives it.
defineProps({ embedded: { type: Boolean, default: false } })

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

const search = ref('')
const visible = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return tokens.value
  return tokens.value.filter((tk) => [tk.name, tk.username, tk.prefix].some((v) => String(v || '').toLowerCase().includes(q)))
})
const inForce = computed(() => tokens.value.filter((tk) => !expired(tk)).length)

const expired = (tk) => !!tk.expiresAt && new Date(tk.expiresAt) <= new Date()
function ends(tk) {
  if (!tk.expiresAt) return t('opTokens.noEnd')
  if (expired(tk)) return t('opTokens.expired')
  return t('opTokens.ends', { d: new Date(tk.expiresAt).toLocaleDateString(store.locale) })
}
</script>

<template>
  <div :class="embedded ? 'tokens-embedded' : 'antpage tokens'">
    <!-- The summary Card: what there is, what is in force, what is left. -->
    <div v-if="!embedded" class="acard small summary-card">
      <div class="acard-body">
        <div class="arow">
          <div class="acol">
            <div class="stat-title">{{ t('opTokens.stat.total') }}</div>
            <div class="stat-content ltr"><span class="stat-prefix"><AntIcon name="KeyOutlined" /></span><span>{{ tokens.length }}</span></div>
          </div>
          <div class="acol">
            <div class="stat-title">{{ t('opTokens.stat.active') }}</div>
            <div class="stat-content ltr"><span class="stat-prefix"><AntIcon name="CheckCircleOutlined" /></span><span>{{ inForce }}</span></div>
          </div>
          <div v-if="issues" class="acol">
            <div class="stat-title">{{ t('opTokens.stat.left') }}</div>
            <div class="stat-content ltr"><span class="stat-prefix"><AntIcon name="PlusCircleOutlined" /></span><span>{{ left }}</span></div>
          </div>
        </div>
      </div>
    </div>

    <!-- The list Card: issue, refresh and search over a table. -->
    <div :class="embedded ? '' : 'acard'">
      <div :class="embedded ? 'tokens-toolbar' : 'acard-head'">
        <div class="aspace">
          <button v-if="issues" class="abtn primary" :disabled="left <= 0" :title="left <= 0 ? t('cap.err.tokens') : ''" @click="createOpen = true">
            <AntIcon name="PlusOutlined" /><span>{{ t('set.security.apiTokenNew') }}</span>
          </button>
          <button class="abtn" :class="{ primary: !issues }" @click="load()"><AntIcon name="ReloadOutlined" /><span>{{ t('action.refresh') }}</span></button>
          <label class="ainput">
            <span class="ainput-prefix"><AntIcon name="SearchOutlined" /></span>
            <input v-model="search" type="text" :placeholder="t('iface.menu.search')" :aria-label="t('iface.menu.search')" />
            <button v-if="search" type="button" class="ainput-clear" :aria-label="t('action.cancel')" @click="search = ''"><AntIcon name="CloseCircleFilled" /></button>
          </label>
        </div>
      </div>
      <div :class="embedded ? '' : 'acard-body'">
        <div class="aalert info with-desc" role="note">
          <AntIcon name="InfoCircleFilled" />
          <div class="aalert-body"><span class="aalert-desc">{{ owner ? t('opTokens.ownerHint') : t('opTokens.hint') }}</span></div>
        </div>
        <div v-if="!visible.length && !loading" class="aempty"><AntIcon name="InboxOutlined" />{{ t('opTokens.empty') }}</div>
        <div v-else-if="visible.length" class="atable-wrap">
          <table class="atable small" style="min-width: 540px">
            <thead>
              <tr>
                <th>{{ t('set.security.apiTokenName') }}</th>
                <th>{{ t('opTokens.col.operator') }}</th>
                <th>{{ t('opTokens.col.ends') }}</th>
                <th>{{ t('opTokens.col.lastUsed') }}</th>
                <th class="col-actions"></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="tk in visible" :key="tk.id" :class="{ 'row-off': expired(tk) }">
                <td>
                  <div class="tk-name">{{ tk.name }}</div>
                  <code class="ltr tk-prefix">{{ tk.prefix }}…</code>
                </td>
                <td>{{ tk.username || '—' }}<span v-if="tk.adminId === me" class="atag" style="margin-inline-start: 6px">{{ t('cap.tokenForMe') }}</span></td>
                <td><span class="atag" :class="expired(tk) ? '' : 'green'">{{ ends(tk) }}</span></td>
                <td class="tk-muted">{{ tk.lastUsedAt ? relative(tk.lastUsedAt, store.locale) : t('settings.tokenNeverUsed') }}</td>
                <td class="col-actions"><button class="abtn text sm" @click="revoking = tk"><AntIcon name="DeleteOutlined" /><span>{{ t('set.delete') }}</span></button></td>
              </tr>
            </tbody>
          </table>
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
.tokens-toolbar { margin-bottom: 12px; }
.tk-name { font-weight: 600; }
.tk-prefix { font-size: 11.5px; color: var(--muted); }
.tk-muted { color: var(--muted); }
.row-off td { opacity: 0.55; }
.col-actions { text-align: end; width: 1%; white-space: nowrap; }
.api-token-created-notice { margin-top: 0; }
.api-token-value-wrap { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
.api-token-value { flex: 1; min-width: 0; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12.5px; padding: 4px 8px; background: var(--surface-3); border-radius: 4px; word-break: break-all; }
.api-token-cost { margin-inline-end: auto; align-self: center; color: var(--muted); font-size: 13px; }
</style>
