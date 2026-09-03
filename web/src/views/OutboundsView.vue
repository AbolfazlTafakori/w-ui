<script setup>
import { computed, onMounted, ref } from 'vue'
import { api } from '../lib/api.js'
import { mergeRows, useDelayed } from '../lib/live.js'
import { t, tn, notify } from '../lib/store.js'
import { bytes } from '../lib/format.js'
import Icon from '../components/Icon.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import OutboundForm from '../components/OutboundForm.vue'

// Where traffic leaves. Two rows always exist and cannot be removed, so a
// routing rule always has somewhere to point.
const outbounds = ref([])
const loading = ref(true)
const loadError = ref('')
const formFor = ref(null) // null = closed, {} = create, { outbound } = edit
const ask = ref(null)
const busy = ref(false)
const mode = ref('tcp')
const checkingAll = ref(false)

// Rows mid-request, so probing one hop does not freeze the controls on another.
const pending = ref(new Set())
const isPending = (id) => pending.value.has(id)
function hold(id) {
  pending.value = new Set(pending.value).add(id)
}
function release(id) {
  const next = new Set(pending.value)
  next.delete(id)
  pending.value = next
}

const hops = computed(() => outbounds.value.filter((o) => !o.builtin))
const totalTraffic = computed(() =>
  outbounds.value.reduce((n, o) => n + (o.txBytes || 0) + (o.rxBytes || 0), 0),
)

async function load(quiet = false) {
  if (!quiet) loading.value = true
  try {
    const fresh = await api.get('/api/outbounds', { background: quiet })
    // Merged rather than replaced: a second switch flipped while the first is
    // still settling would otherwise be overwritten by the first one's refresh.
    outbounds.value = quiet ? mergeRows(outbounds.value, fresh, pending.value) : fresh
    loadError.value = ''
  } catch (err) {
    loadError.value = err.message
  } finally {
    loading.value = false
  }
}

const showSkeleton = useDelayed(computed(() => loading.value && !outbounds.value.length))

onMounted(load)

function kindLabel(o) {
  return t(`outbound.kind.${o.kind}`)
}

// A latency figure is only meaningful next to when it was taken. One from an
// hour ago next to a hop that has since died reads as proof it is fine.
function checkedAgo(o) {
  if (!o.lastCheckAt) return ''
  const secs = Math.max(0, (Date.now() - new Date(o.lastCheckAt).getTime()) / 1000)
  if (secs < 60) return t('time.justNow')
  if (secs < 3600) return tn('time.minutesAgo', Math.floor(secs / 60))
  return tn('time.hoursAgo', Math.floor(secs / 3600))
}

async function check(o) {
  hold(o.id)
  try {
    const res = await api.post(`/api/outbounds/${o.id}/check?mode=${mode.value}`)
    Object.assign(o, {
      latencyMs: res.latencyMs,
      lastError: res.ok ? '' : res.error,
      lastCheckAt: new Date().toISOString(),
    })
    if (!res.ok) notify(`${o.tag}: ${res.error}`, 'error')
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    release(o.id)
  }
}

async function checkAll() {
  checkingAll.value = true
  outbounds.value.forEach((o) => hold(o.id))
  try {
    await api.post(`/api/outbounds/check?mode=${mode.value}`)
    await load(true)
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    outbounds.value.forEach((o) => release(o.id))
    checkingAll.value = false
  }
}

async function setEnabled(o, on) {
  const was = o.enabled
  if (was === on) return
  o.enabled = on
  hold(o.id)
  try {
    const updated = await api.patch(`/api/outbounds/${o.id}`, {
      tag: o.tag,
      kind: o.kind,
      address: o.address,
      enabled: on,
    })
    Object.assign(o, updated)
  } catch (err) {
    o.enabled = was
    notify(err.message, 'error')
  } finally {
    release(o.id)
    load(true)
  }
}

function remove(o) {
  ask.value = {
    title: t('outbound.removeTitle'),
    body: t('outbound.removeBody'),
    subject: o.tag,
    consequences: [t('outbound.removeConsequence')],
    confirmLabel: t('action.delete'),
    run: async () => {
      await api.delete(`/api/outbounds/${o.id}`)
      notify(t('outbound.removed'), 'success')
      await load()
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
    notify(err.message, 'error')
  } finally {
    busy.value = false
  }
}

async function onSaved() {
  formFor.value = null
  await load()
}
</script>

<template>
  <section class="view">
    <header class="page-head">
      <div>
        <h1>{{ t('nav.outbounds') }}</h1>
        <p class="muted">{{ t('outbound.lede') }}</p>
      </div>
    </header>

    <div class="statbar">
      <div class="stat">
        <span class="stat-label">{{ t('outbound.stat.total') }}</span>
        <span class="stat-value">{{ outbounds.length }}</span>
      </div>
      <div class="stat">
        <span class="stat-label">{{ t('outbound.stat.hops') }}</span>
        <span class="stat-value">{{ hops.length }}</span>
      </div>
      <div class="stat">
        <span class="stat-label">{{ t('outbound.stat.traffic') }}</span>
        <span class="stat-value">{{ bytes(totalTraffic) }}</span>
      </div>
    </div>

    <div class="toolbar">
      <button class="btn primary" @click="formFor = {}">
        <Icon name="plus" :size="16" /> {{ t('outbound.add') }}
      </button>

      <div class="spacer"></div>

      <!-- How to measure. A segmented control rather than a dropdown: there are
           two choices and the current one should be readable without opening
           anything. -->
      <div class="segmented" role="group" :aria-label="t('outbound.checkMode')">
        <button
          type="button"
          :class="{ on: mode === 'tcp' }"
          :aria-pressed="mode === 'tcp'"
          @click="mode = 'tcp'"
        >
          TCP
        </button>
        <button
          type="button"
          :class="{ on: mode === 'http' }"
          :aria-pressed="mode === 'http'"
          @click="mode = 'http'"
        >
          HTTP
        </button>
      </div>

      <button class="btn" :disabled="checkingAll" @click="checkAll">
        <span v-if="checkingAll" class="spin sm"></span>
        <Icon v-else name="refresh" :size="16" />
        {{ t('outbound.checkAll') }}
      </button>
    </div>

    <div v-if="loadError" class="empty empty-cta">
      <Icon name="alert" :size="28" />
      <p>{{ loadError }}</p>
      <button class="btn" @click="load()">{{ t('action.retry') }}</button>
    </div>

    <table v-else-if="showSkeleton" class="skeleton" aria-hidden="true">
      <tbody>
        <tr v-for="n in 4" :key="n">
          <td v-for="c in 7" :key="c"><span class="sk"></span></td>
        </tr>
      </tbody>
    </table>
    <div v-else-if="loading" class="empty"></div>

    <div v-else class="card">
      <table class="table">
        <thead>
          <tr>
            <th class="w-gact">{{ t('table.actions') }}</th>
            <th>{{ t('table.enabled') }}</th>
            <th>{{ t('outbound.tag') }}</th>
            <th>{{ t('outbound.address') }}</th>
            <th class="num">{{ t('outbound.traffic') }}</th>
            <th class="num">{{ t('outbound.latency') }}</th>
            <th class="right">{{ t('outbound.check') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="o in outbounds" :key="o.id">
            <td class="w-gact">
              <div class="actions">
                <button
                  class="act"
                  :title="t('action.edit')"
                  :disabled="isPending(o.id)"
                  @click="formFor = { outbound: o }"
                >
                  <Icon name="edit" :size="16" />
                </button>
                <button
                  class="act danger"
                  :title="o.builtin ? t('outbound.builtinLocked') : t('action.delete')"
                  :disabled="o.builtin || isPending(o.id)"
                  @click="remove(o)"
                >
                  <Icon name="trash" :size="16" />
                </button>
              </div>
            </td>

            <td>
              <input
                type="checkbox"
                :checked="o.enabled"
                :disabled="o.builtin || isPending(o.id)"
                :aria-label="o.tag"
                @change="setEnabled(o, $event.target.checked)"
              />
            </td>

            <td>
              <div class="stack">
                <strong>{{ o.tag }}</strong>
                <span class="tag" :class="o.builtin ? 'geekblue' : 'green'">
                  {{ kindLabel(o) }}
                </span>
              </div>
              <div v-if="o.note" class="muted small">{{ o.note }}</div>
            </td>

            <td class="ltr">
              <span v-if="o.address">{{ o.address }}</span>
              <span v-else class="muted">—</span>
            </td>

            <td class="num ltr">
              <span v-if="o.txBytes || o.rxBytes">
                ↑ {{ bytes(o.txBytes) }} ↓ {{ bytes(o.rxBytes) }}
              </span>
              <span v-else class="muted">—</span>
            </td>

            <td class="num ltr">
              <template v-if="o.lastError">
                <span class="tag red" :title="o.lastError">{{ t('outbound.failed') }}</span>
              </template>
              <template v-else-if="o.latencyMs">
                <span class="tag green">{{ o.latencyMs }} ms</span>
                <div class="muted small">{{ checkedAgo(o) }}</div>
              </template>
              <span v-else class="muted">—</span>
            </td>

            <td class="right">
              <button
                class="act"
                :title="t('outbound.checkOne')"
                :disabled="isPending(o.id)"
                @click="check(o)"
              >
                <span v-if="isPending(o.id)" class="spin sm"></span>
                <Icon v-else name="zap" :size="16" />
              </button>
            </td>
          </tr>
        </tbody>
      </table>

      <div v-if="!hops.length" class="empty empty-cta">
        <Icon name="outbound" :size="28" />
        <p>{{ t('outbound.emptyHint') }}</p>
        <button class="btn primary" @click="formFor = {}">{{ t('outbound.add') }}</button>
      </div>
    </div>

    <OutboundForm
      v-if="formFor"
      :outbound="formFor.outbound"
      @saved="onSaved"
      @cancel="formFor = null"
    />

    <ConfirmDialog
      :open="!!ask"
      :title="ask?.title || ''"
      :body="ask?.body || ''"
      :subject="ask?.subject || ''"
      :consequences="ask?.consequences || []"
      :confirm-label="ask?.confirmLabel || ''"
      :busy="busy"
      @confirm="runConfirmed"
      @cancel="ask = null"
    />
  </section>
</template>
