<script setup>
// Who else signs in to this panel.
//
// The page is the owner's alone -- the route guard keeps it off everyone
// else's menu and every endpoint behind it refuses them again -- and what it
// administers is resale: a reseller gets a username, a password, the servers
// they may sell, and a ceiling of customers, traffic and time. Switching one
// off stops every customer they hold without touching those customers' own
// switches, so switching them back on restores exactly what was running.
import { computed, onMounted, ref } from 'vue'
import { api } from '../lib/api.js'
import { store, t, notify } from '../lib/store.js'
import { bytes, bytesToGigabytes, gigabytesToBytes, date } from '../lib/format.js'
import Icon from '../components/Icon.vue'
import ErrorState from '../components/ErrorState.vue'
import PageSpin from '../components/PageSpin.vue'

const items = ref([])
const servers = ref([])
const loading = ref(true)
const loadError = ref(null)
const busy = ref(false)

// The form, open for a new operator or for one being edited.
const form = ref(null)
const fieldError = ref({})
const removing = ref(null)

const nf = (n) => Number(n || 0).toLocaleString(store.locale)

async function load() {
  loading.value = true
  loadError.value = null
  try {
    const [list, ifaces] = await Promise.all([api.admins(), api.interfaces()])
    items.value = list.items || []
    servers.value = ifaces || []
  } catch (err) {
    loadError.value = err
  } finally {
    loading.value = false
  }
}
onMounted(load)

function blank() {
  return {
    id: 0,
    username: '',
    password: '',
    note: '',
    role: 'reseller',
    enabled: true,
    clientLimit: 0,
    quotaGb: 0,
    expiresAt: '',
    interfaceIds: [],
  }
}

function openCreate() {
  fieldError.value = {}
  form.value = blank()
}

function openEdit(a) {
  fieldError.value = {}
  form.value = {
    id: a.id,
    username: a.username,
    // Never prefilled: the stored one cannot be read back, and a box that
    // looked full would be saved unchanged and read as "the password still
    // works" when it had just been cleared.
    password: '',
    note: a.note || '',
    role: a.role,
    enabled: a.enabled,
    clientLimit: a.clientLimit || 0,
    quotaGb: a.quotaBytes ? bytesToGigabytes(a.quotaBytes) : 0,
    expiresAt: a.expiresAt ? String(a.expiresAt).slice(0, 10) : '',
    interfaceIds: [...(a.interfaceIds || [])],
  }
}

const editing = computed(() => !!form.value?.id)
const capped = computed(() => form.value?.role === 'reseller')

function toggleServer(id) {
  const list = form.value.interfaceIds
  const at = list.indexOf(id)
  if (at < 0) list.push(id)
  else list.splice(at, 1)
}

async function save() {
  if (!form.value) return
  busy.value = true
  fieldError.value = {}
  const f = form.value
  const body = {
    username: f.username.trim(),
    note: f.note,
    role: f.role,
    enabled: f.enabled,
    interfaceIds: capped.value ? f.interfaceIds : [],
  }
  if (f.password) body.password = f.password
  if (capped.value) {
    body.clientLimit = Number(f.clientLimit) || 0
    body.quotaBytes = gigabytesToBytes(Number(f.quotaGb) || 0)
    // An empty date is "no end", which has to be sent as null rather than
    // left out: left out means "leave it as it was", and clearing the date
    // is a thing an owner does.
    body.expiresAt = f.expiresAt ? new Date(`${f.expiresAt}T00:00:00`).toISOString() : null
  }
  try {
    if (f.id) await api.updateAdmin(f.id, body)
    else await api.createAdmin(body)
    form.value = null
    await load()
    notify(t('admins.saved'), 'success')
  } catch (err) {
    if (err.field) fieldError.value = { [err.field]: err.message }
    else notify(err.message, 'error')
  } finally {
    busy.value = false
  }
}

async function setEnabled(a, on) {
  busy.value = true
  try {
    await api.updateAdmin(a.id, { enabled: on })
    await load()
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = false
  }
}

async function resetUsage(a) {
  busy.value = true
  try {
    await api.resetAdminUsage(a.id)
    await load()
    notify(t('admins.usageReset'), 'success')
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = false
  }
}

async function remove(mode) {
  const a = removing.value
  if (!a) return
  busy.value = true
  try {
    await api.deleteAdmin(a.id, mode)
    removing.value = null
    await load()
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = false
  }
}

function roleLabel(role) {
  return t(`admins.role.${role}`)
}

// How much of the ceiling is gone, for the bar on the row. Unlimited draws
// nothing rather than a full bar or an empty one, both of which read as a
// number somebody should act on.
function spent(a) {
  if (!a.quotaBytes) return null
  return Math.min(100, Math.round((a.usedBytes / a.quotaBytes) * 100))
}

function serverName(id) {
  return servers.value.find((s) => s.id === id)?.name || `#${id}`
}
</script>

<template>
  <div class="card">
    <div class="card-toolbar">
      <button class="btn primary" @click="openCreate">
        <Icon name="plus" :size="14" />
        <span>{{ t('admins.add') }}</span>
      </button>
      <span class="muted small">{{ t('admins.lede') }}</span>
    </div>

    <ErrorState v-if="loadError" :error="loadError" @retry="load" />
    <PageSpin v-else-if="loading" />

    <div v-else class="table-wrap">
      <table>
        <thead>
          <tr>
            <th class="w-gact">{{ t('table.actions') }}</th>
            <th>{{ t('admins.username') }}</th>
            <th>{{ t('admins.roleColumn') }}</th>
            <th class="w-gcount">{{ t('admins.customers') }}</th>
            <th>{{ t('admins.allowance') }}</th>
            <th>{{ t('admins.until') }}</th>
            <th>{{ t('admins.servers') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="a in items" :key="a.id" :class="{ off: !a.enabled }">
            <td class="w-gact">
              <div class="actions">
                <button
                  v-if="a.role !== 'owner'"
                  class="act"
                  :title="t('admins.edit')"
                  @click="openEdit(a)"
                >
                  <Icon name="edit" :size="16" />
                </button>
                <button
                  v-if="a.role === 'reseller'"
                  class="act"
                  :title="a.enabled ? t('admins.switchOff') : t('admins.switchOn')"
                  :disabled="busy"
                  @click="setEnabled(a, !a.enabled)"
                >
                  <Icon :name="a.enabled ? 'pause' : 'play'" :size="16" />
                </button>
                <button
                  v-if="a.role === 'reseller' && a.quotaBytes"
                  class="act"
                  :title="t('admins.resetUsage')"
                  :disabled="busy"
                  @click="resetUsage(a)"
                >
                  <Icon name="refresh" :size="16" />
                </button>
                <button
                  v-if="a.role !== 'owner'"
                  class="act danger"
                  :title="t('admins.remove')"
                  @click="removing = a"
                >
                  <Icon name="trash" :size="16" />
                </button>
              </div>
            </td>

            <td>
              <span class="name">{{ a.username }}</span>
              <div v-if="a.note" class="sub muted small">{{ a.note }}</div>
              <div v-if="a.groupName" class="sub muted small">
                {{ t('admins.filedUnder') }} <span class="tag">{{ a.groupName }}</span>
              </div>
            </td>

            <td>
              <span class="tag" :class="a.role === 'owner' ? 'gold' : a.role === 'admin' ? 'geekblue' : ''">
                {{ roleLabel(a.role) }}
              </span>
              <div v-if="a.role !== 'owner' && !a.enabled" class="sub muted small">
                {{ t('admins.switchedOff') }}
              </div>
            </td>

            <td class="num">
              {{ nf(a.clients) }}<span v-if="a.clientLimit" class="muted"> / {{ nf(a.clientLimit) }}</span>
            </td>

            <td>
              <template v-if="a.role === 'reseller'">
                <span class="num ltr">{{ bytes(a.usedBytes, store.locale) }}</span>
                <span v-if="a.quotaBytes" class="muted num ltr"> / {{ bytes(a.quotaBytes, store.locale) }}</span>
                <span v-else class="muted small"> · {{ t('admins.unlimited') }}</span>
                <div v-if="spent(a) !== null" class="meter">
                  <div class="meter-fill" :class="{ warn: spent(a) >= 90 }" :style="{ width: spent(a) + '%' }"></div>
                </div>
              </template>
              <span v-else class="muted small">—</span>
            </td>

            <td>
              <span v-if="a.expiresAt">{{ date(a.expiresAt, store.locale) }}</span>
              <span v-else class="muted small">—</span>
            </td>

            <td>
              <template v-if="a.role === 'reseller'">
                <span v-if="!a.interfaceIds?.length" class="muted small">{{ t('admins.noServers') }}</span>
                <span v-for="id in a.interfaceIds" :key="id" class="tag">{{ serverName(id) }}</span>
              </template>
              <span v-else class="muted small">{{ t('admins.allServers') }}</span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>

  <!-- The form -->
  <div v-if="form" class="modal-backdrop" @click.self="form = null">
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="a-title">
      <div class="card-head">
        <h2 id="a-title">{{ editing ? t('admins.edit') : t('admins.add') }}</h2>
        <button class="btn sm icon ghost spacer" :aria-label="t('action.cancel')" @click="form = null">
          <Icon name="close" :size="15" />
        </button>
      </div>

      <div class="card-body">
        <div class="field">
          <label for="a-user">{{ t('admins.username') }}</label>
          <input id="a-user" v-model="form.username" autocomplete="off" />
          <p v-if="fieldError.username" class="field-error">{{ fieldError.username }}</p>
        </div>

        <div class="field">
          <label for="a-pass">{{ editing ? t('admins.newPassword') : t('admins.password') }}</label>
          <input id="a-pass" v-model="form.password" type="password" autocomplete="new-password" />
          <p class="muted small">{{ editing ? t('admins.passwordHintEdit') : t('admins.passwordHint') }}</p>
          <p v-if="fieldError.password" class="field-error">{{ fieldError.password }}</p>
        </div>

        <div class="field">
          <label for="a-role">{{ t('admins.roleColumn') }}</label>
          <select id="a-role" v-model="form.role">
            <option value="reseller">{{ t('admins.role.reseller') }}</option>
            <option value="admin">{{ t('admins.role.admin') }}</option>
          </select>
          <p class="muted small">
            {{ capped ? t('admins.role.resellerHint') : t('admins.role.adminHint') }}
          </p>
          <p v-if="fieldError.role" class="field-error">{{ fieldError.role }}</p>
        </div>

        <div class="field">
          <label class="check">
            <input v-model="form.enabled" type="checkbox" />
            <span>{{ t('admins.enabled') }}</span>
          </label>
          <p class="muted small">{{ t('admins.enabledHint') }}</p>
        </div>

        <template v-if="capped">
          <div class="field">
            <label>{{ t('admins.servers') }}</label>
            <p class="muted small">{{ t('admins.serversHint') }}</p>
            <div class="chips">
              <button
                v-for="s in servers"
                :key="s.id"
                type="button"
                class="tag pick"
                :class="{ on: form.interfaceIds.includes(s.id) }"
                @click="toggleServer(s.id)"
              >
                {{ s.name }}
              </button>
            </div>
            <p v-if="fieldError.interfaceIds" class="field-error">{{ fieldError.interfaceIds }}</p>
          </div>

          <div class="field">
            <label for="a-limit">{{ t('admins.clientLimit') }}</label>
            <input id="a-limit" v-model.number="form.clientLimit" type="number" min="0" />
            <p class="muted small">{{ t('admins.zeroMeansNoLimit') }}</p>
          </div>

          <div class="field">
            <label for="a-quota">{{ t('admins.quotaGb') }}</label>
            <input id="a-quota" v-model.number="form.quotaGb" type="number" min="0" step="1" />
            <p class="muted small">{{ t('admins.quotaHint') }}</p>
          </div>

          <div class="field">
            <label for="a-expires">{{ t('admins.until') }}</label>
            <input id="a-expires" v-model="form.expiresAt" type="date" />
            <p class="muted small">{{ t('admins.untilHint') }}</p>
          </div>
        </template>

        <div class="field">
          <label for="a-note">{{ t('admins.note') }}</label>
          <input id="a-note" v-model="form.note" />
        </div>
      </div>

      <div class="card-foot">
        <button class="btn ghost" @click="form = null">{{ t('action.cancel') }}</button>
        <button class="btn primary" :disabled="busy" @click="save">{{ t('action.save') }}</button>
      </div>
    </div>
  </div>

  <!-- Removing one. What becomes of their customers is asked rather than
       assumed: one answer quietly stops people paying for a service and the
       other quietly leaves the owner administering customers they did not
       know they had. -->
  <div v-if="removing" class="modal-backdrop" @click.self="removing = null">
    <div class="modal narrow" role="alertdialog" aria-modal="true" aria-labelledby="r-title">
      <div class="card-body">
        <h2 id="r-title">{{ t('admins.remove') }}</h2>
        <p class="confirm-text">{{ t('admins.removeAsk', { name: removing.username }) }}</p>
        <p class="confirm-subject">
          <span class="tag red">{{ t('admins.holdsCustomers', { n: removing.clients || 0 }) }}</span>
        </p>
      </div>
      <div class="modal-foot">
        <button class="btn ghost" @click="removing = null">{{ t('action.cancel') }}</button>
        <button class="btn" :disabled="busy" @click="remove('keep')">{{ t('admins.removeKeep') }}</button>
        <button class="btn danger" :disabled="busy" @click="remove('delete')">
          {{ t('admins.removeWithClients') }}
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
tr.off .name {
  opacity: 0.55;
}
.chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.tag.pick {
  cursor: pointer;
  border: 1px solid var(--border, #d9d9d9);
  background: transparent;
}
.tag.pick.on {
  border-color: var(--primary, #1677ff);
  color: var(--primary, #1677ff);
}
.meter {
  margin-top: 4px;
  height: 4px;
  border-radius: 2px;
  background: var(--border, #e8e8e8);
  overflow: hidden;
}
.meter-fill {
  height: 100%;
  background: var(--primary, #1677ff);
}
.meter-fill.warn {
  background: var(--warning, #faad14);
}
</style>
