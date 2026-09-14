<script setup>
import { computed, onMounted, ref } from 'vue'
import { api } from '../lib/api.js'
import { t, notify } from '../lib/store.js'
import Icon from './Icon.vue'
import Toggle from './Toggle.vue'
import ConfirmDialog from './ConfirmDialog.vue'

// The Outbound Subscriptions dialog, laid out the way 3x-ui lays its own out:
// the form on top -- Remark, URL, Tag prefix, Update interval, Enabled, Allow
// private address, Before manual outbounds -- with Add, Preview and Cancel
// under it; then the active subscriptions as a table with move, edit,
// refresh-now and delete on each row, Refresh all above it, and the hint about
// when the outbounds become active under it.
//
// Their "Allow insecure" switch is not here: it turns off TLS verification on
// an Xray outbound, and the proxies a subscription produces for this panel
// carry no TLS of their own to verify.

const emit = defineEmits(['close', 'changed'])

const subs = ref([])
const loading = ref(true)
const busy = ref(false)
const refreshing = ref(new Set())
const editingId = ref(null)
const preview = ref(null) // null = none, [] = fetched
const ask = ref(null)

function blank() {
  return {
    remark: '',
    url: '',
    tagPrefix: '',
    hours: 0,
    minutes: 10,
    enabled: true,
    allowPrivate: false,
    prepend: false,
  }
}
const form = ref(blank())

const intervalMin = computed(() => {
  const n = Number(form.value.hours || 0) * 60 + Number(form.value.minutes || 0)
  return n > 0 ? n : 10
})

async function load() {
  loading.value = true
  try {
    subs.value = await api.get('/api/outbound-subs')
  } catch {
    notify(t('outbound.sub.toastLoadFailed'), 'error')
  } finally {
    loading.value = false
  }
}
onMounted(load)

function payload() {
  return {
    remark: form.value.remark.trim(),
    url: form.value.url.trim(),
    tagPrefix: form.value.tagPrefix.trim(),
    intervalMin: intervalMin.value,
    enabled: !!form.value.enabled,
    allowPrivate: !!form.value.allowPrivate,
    prepend: !!form.value.prepend,
  }
}

function resetForm() {
  form.value = blank()
  editingId.value = null
  preview.value = null
}

function edit(sub) {
  editingId.value = sub.id
  form.value = {
    remark: sub.remark || '',
    url: sub.url,
    tagPrefix: sub.tagPrefix || '',
    hours: Math.floor(sub.intervalMin / 60),
    minutes: sub.intervalMin % 60,
    enabled: sub.enabled,
    allowPrivate: sub.allowPrivate,
    prepend: sub.prepend,
  }
  preview.value = null
}

async function submit() {
  if (!form.value.url.trim()) {
    notify(t('outbound.sub.toastUrlRequired'), 'warning')
    return
  }
  busy.value = true
  try {
    if (editingId.value != null) {
      await api.patch(`/api/outbound-subs/${editingId.value}`, payload())
      notify(t('outbound.sub.toastUpdated'), 'success')
    } else {
      await api.post('/api/outbound-subs', payload())
      notify(t('outbound.sub.toastAdded'), 'success')
    }
    resetForm()
    await load()
    emit('changed')
  } catch (err) {
    notify(err.message || t('outbound.sub.toastAddFailed'), 'error')
  } finally {
    busy.value = false
  }
}

async function doPreview() {
  if (!form.value.url.trim()) {
    notify(t('outbound.sub.toastUrlRequired'), 'warning')
    return
  }
  busy.value = true
  try {
    const items = await api.post('/api/outbound-subs/preview', payload())
    preview.value = items
    if (!items.length) notify(t('outbound.sub.previewEmpty'), 'info')
  } catch (err) {
    notify(err.message || t('outbound.sub.previewEmpty'), 'error')
  } finally {
    busy.value = false
  }
}

async function refresh(sub) {
  refreshing.value = new Set(refreshing.value).add(sub.id)
  try {
    subs.value = await api.post(`/api/outbound-subs/${sub.id}/refresh`)
    notify(t('outbound.sub.toastRefreshed'), 'success')
    emit('changed')
  } catch (err) {
    notify(err.message || t('outbound.sub.toastRefreshFailed'), 'error')
    await load()
  } finally {
    const next = new Set(refreshing.value)
    next.delete(sub.id)
    refreshing.value = next
  }
}

async function refreshAll() {
  busy.value = true
  try {
    subs.value = await api.post('/api/outbound-subs/refresh')
    notify(t('outbound.sub.toastRefreshed'), 'success')
    emit('changed')
  } catch (err) {
    notify(err.message || t('outbound.sub.toastRefreshFailed'), 'error')
    await load()
  } finally {
    busy.value = false
  }
}

async function setEnabled(sub, on) {
  const was = sub.enabled
  sub.enabled = on
  try {
    await api.patch(`/api/outbound-subs/${sub.id}`, {
      remark: sub.remark,
      url: sub.url,
      tagPrefix: sub.tagPrefix,
      intervalMin: sub.intervalMin,
      enabled: on,
      allowPrivate: sub.allowPrivate,
      prepend: sub.prepend,
    })
    emit('changed')
  } catch (err) {
    sub.enabled = was
    notify(err.message, 'error')
  }
}

async function move(idx, dir) {
  const ids = subs.value.map((s) => s.id)
  const to = idx + dir
  if (to < 0 || to >= ids.length) return
  ;[ids[idx], ids[to]] = [ids[to], ids[idx]]
  try {
    subs.value = await api.post('/api/outbound-subs/order', { ids })
  } catch (err) {
    notify(err.message, 'error')
  }
}

function remove(sub) {
  ask.value = {
    title: t('outbound.sub.deleteConfirm'),
    body: '',
    subject: sub.remark || sub.url,
    confirmLabel: t('action.delete'),
    run: async () => {
      await api.del(`/api/outbound-subs/${sub.id}`)
      notify(t('outbound.sub.toastDeleted'), 'success')
      await load()
      emit('changed')
    },
  }
}

async function runConfirmed() {
  if (!ask.value) return
  busy.value = true
  try {
    await ask.value.run()
    ask.value = null
  } catch (err) {
    notify(err.message || t('outbound.sub.toastDeleteFailed'), 'error')
    ask.value = null
    await load()
  } finally {
    busy.value = false
  }
}

function when(v) {
  return v ? new Date(v).toLocaleString() : t('outbound.sub.never')
}
</script>

<template>
  <div class="modal-backdrop" @click.self="emit('close')">
    <div class="modal wide" role="dialog" aria-modal="true" aria-labelledby="obsub-title">
      <div class="card-head">
        <h2 id="obsub-title">{{ t('outbound.sub.title') }}</h2>
        <button class="act" :title="t('common.close')" @click="emit('close')">
          <Icon name="close" :size="16" />
        </button>
      </div>

      <div class="card-body">
        <form class="form sub-form" @submit.prevent="submit">
          <p v-if="editingId != null" class="editing">
            <span class="tag blue">{{ t('action.edit') }}</span>
          </p>

          <div class="field">
            <label for="obsub-remark">{{ t('outbound.sub.remark') }}</label>
            <input id="obsub-remark" v-model="form.remark" maxlength="64" :placeholder="t('outbound.sub.remarkPlaceholder')" />
          </div>
          <div class="field">
            <label for="obsub-url">{{ t('outbound.sub.url') }} <span class="req">*</span></label>
            <input id="obsub-url" v-model="form.url" class="ltr" :placeholder="t('outbound.sub.urlPlaceholder')" spellcheck="false" />
          </div>
          <div class="field">
            <label for="obsub-prefix">{{ t('outbound.sub.tagPrefix') }}</label>
            <input id="obsub-prefix" v-model="form.tagPrefix" class="ltr" maxlength="32" :placeholder="t('outbound.sub.tagPrefixPlaceholder')" />
          </div>
          <div class="field">
            <label>{{ t('outbound.sub.interval') }}</label>
            <div class="interval">
              <input v-model.number="form.hours" type="number" min="0" max="168" class="ltr" />
              <span class="unit">{{ t('outbound.sub.hours') }}</span>
              <input v-model.number="form.minutes" type="number" min="0" max="59" class="ltr" />
              <span class="unit">{{ t('outbound.sub.minutes') }}</span>
            </div>
            <span class="hint">{{ t('outbound.sub.intervalHint') }}</span>
          </div>
          <div class="field row">
            <label>{{ t('outbound.sub.enabled') }}</label>
            <Toggle v-model="form.enabled" :label="t('outbound.sub.enabled')" />
          </div>
          <div class="field row">
            <label>{{ t('outbound.sub.allowPrivate') }}</label>
            <Toggle v-model="form.allowPrivate" :label="t('outbound.sub.allowPrivate')" />
            <span class="hint">{{ t('outbound.sub.allowPrivateHint') }}</span>
          </div>
          <div class="field row">
            <label>{{ t('outbound.sub.prepend') }}</label>
            <Toggle v-model="form.prepend" :label="t('outbound.sub.prepend')" />
            <span class="hint">{{ t('outbound.sub.prependHint') }}</span>
          </div>

          <div class="btn-row">
            <button type="submit" class="btn primary" :disabled="busy">
              <span v-if="busy" class="spin sm"></span>
              <span>{{ editingId != null ? t('action.save') : t('outbound.sub.addButton') }}</span>
            </button>
            <button type="button" class="btn" :disabled="busy" @click="doPreview">
              {{ t('outbound.sub.preview') }}
            </button>
            <button v-if="editingId != null" type="button" class="btn" @click="resetForm">
              {{ t('action.cancel') }}
            </button>
          </div>
        </form>

        <div v-if="preview && preview.length" class="preview">
          <div class="section-title">{{ preview.length }} · {{ t('nav.outbounds') }}</div>
          <div class="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>{{ t('outbound.tag') }}</th>
                  <th>{{ t('outbound.kind') }}</th>
                  <th>{{ t('outbound.address') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="(it, i) in preview" :key="i">
                  <td><strong>{{ it.tag }}</strong></td>
                  <td><span class="tag green">{{ t(`outbound.kind.${it.kind}`) }}</span></td>
                  <td class="ltr">{{ it.address }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>

        <div class="active-head">
          <span class="section-title">{{ t('outbound.sub.active') }}</span>
          <span class="spacer"></span>
          <button class="act" :aria-label="t('action.refresh')" :title="t('action.refresh')" :disabled="loading" @click="load">
            <Icon name="refresh" :size="14" />
          </button>
          <button class="btn sm" :disabled="busy || !subs.length" @click="refreshAll">
            <Icon name="refresh" :size="13" />
            <span>{{ t('outbound.sub.refreshAll') }}</span>
          </button>
        </div>

        <div v-if="!loading && !subs.length" class="muted empty-note">{{ t('outbound.sub.empty') }}</div>

        <div v-else class="table-wrap">
          <table>
            <thead>
              <tr>
                <th class="w-move"></th>
                <th>{{ t('outbound.sub.colRemark') }}</th>
                <th class="num">{{ t('nav.outbounds') }}</th>
                <th>{{ t('common.status') }}</th>
                <th>{{ t('outbound.sub.colLastFetch') }}</th>
                <th>{{ t('outbound.sub.colEnabled') }}</th>
                <th class="right">{{ t('table.actions') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(s, idx) in subs" :key="s.id">
                <td class="w-move">
                  <div class="actions">
                    <button class="act" :aria-label="t('outbound.moveUp')" :disabled="idx === 0" @click="move(idx, -1)">
                      <Icon name="chevronDown" :size="14" class="flip" />
                    </button>
                    <button class="act" :aria-label="t('outbound.moveDown')" :disabled="idx === subs.length - 1" @click="move(idx, 1)">
                      <Icon name="chevronDown" :size="14" />
                    </button>
                  </div>
                </td>
                <td>
                  <div>{{ s.remark || '' }}<em v-if="!s.remark" class="muted">{{ t('outbound.sub.auto') }}</em></div>
                  <div class="muted small ltr url">{{ s.url }}</div>
                </td>
                <td class="num">{{ s.count }}</td>
                <td>
                  <span v-if="s.lastError" class="tag red" :title="s.lastError">{{ t('outbound.failed') }}</span>
                  <span v-else-if="s.lastFetchAt" class="tag green" :title="t('outbound.sub.statusOk')">{{ t('outbound.sub.statusOk') }}</span>
                  <span v-else class="muted">—</span>
                </td>
                <td class="small">{{ when(s.lastFetchAt) }}</td>
                <td>
                  <Toggle :model-value="s.enabled" :label="s.remark || s.url" @update:model-value="setEnabled(s, $event)" />
                </td>
                <td class="right">
                  <div class="actions end">
                    <button class="act" :aria-label="t('action.edit')" :title="t('action.edit')" @click="edit(s)">
                      <Icon name="edit" :size="14" />
                    </button>
                    <button class="act" :aria-label="t('outbound.sub.refreshNow')" :title="t('outbound.sub.refreshNow')" :disabled="refreshing.has(s.id)" @click="refresh(s)">
                      <span v-if="refreshing.has(s.id)" class="spin sm"></span>
                      <Icon v-else name="refresh" :size="14" />
                    </button>
                    <button class="act danger" :aria-label="t('action.delete')" :title="t('action.delete')" @click="remove(s)">
                      <Icon name="trash" :size="14" />
                    </button>
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <p class="muted small restart-hint">{{ t('outbound.sub.restartHint') }}</p>
      </div>
    </div>

    <ConfirmDialog
      :open="!!ask"
      :title="ask?.title || ''"
      :body="ask?.body || ''"
      :subject="ask?.subject || ''"
      :confirm-label="ask?.confirmLabel || ''"
      :busy="busy"
      @confirm="runConfirmed"
      @cancel="ask = null"
    />
  </div>
</template>

<style scoped>
.modal.wide {
  max-width: 900px;
}
.card-body {
  padding: 18px;
}
.sub-form {
  margin-bottom: 18px;
}
.editing {
  margin: 0 0 12px;
}
.interval {
  display: flex;
  align-items: center;
  gap: 8px;
}
.interval input {
  width: 90px;
}
.unit {
  color: var(--muted);
  font-size: var(--t-sm);
}
.field.row {
  display: grid;
  grid-template-columns: 1fr auto;
  align-items: center;
  gap: 4px 12px;
}
.field.row .hint {
  grid-column: 1 / -1;
}
.btn-row {
  display: flex;
  gap: 8px;
  margin-top: 8px;
}
.section-title {
  font-weight: 600;
}
.preview {
  margin-bottom: 18px;
}
.preview .section-title {
  display: block;
  margin-bottom: 8px;
}
.active-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
.spacer {
  flex: 1;
}
.empty-note {
  padding: 12px 0;
}
.w-move {
  width: 1%;
  white-space: nowrap;
}
.actions {
  display: flex;
  gap: 2px;
}
.actions.end {
  justify-content: flex-end;
}
.url {
  max-width: 320px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.restart-hint {
  margin: 12px 0 0;
}
</style>
