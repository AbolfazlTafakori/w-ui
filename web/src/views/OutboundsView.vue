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
    <!-- No page heading and no figures. 3x-ui opens this page on the toolbar:
         the controls on the left add and manage, the ones on the right test.
         The row is justify-content: space-between, the way theirs is. -->
    <div class="card">
      <div class="card-toolbar spread wrap">
        <div class="toolbar-group">
          <button class="btn primary" @click="formFor = {}">
            <Icon name="plus" :size="14" />
            <span>{{ t('nav.outbounds') }}</span>
          </button>
        </div>

        <div class="toolbar-group">
          <!-- How to measure. Their Radio.Group buttonStyle="solid"
               size="small": one joined control, the chosen mode filled. -->
          <div class="seg sm" role="group" :aria-label="t('outbound.checkMode')">
            <button
              type="button"
              class="seg-btn"
              :class="{ on: mode === 'tcp' }"
              :aria-pressed="mode === 'tcp'"
              @click="mode = 'tcp'"
            >
              TCP
            </button>
            <button
              type="button"
              class="seg-btn"
              :class="{ on: mode === 'http' }"
              :aria-pressed="mode === 'http'"
              @click="mode = 'http'"
            >
              HTTP
            </button>
          </div>

          <button class="btn primary" :disabled="checkingAll" @click="checkAll">
            <span v-if="checkingAll" class="spin sm"></span>
            <Icon v-else name="play" :size="14" />
            <span>{{ t('outbound.checkAll') }}</span>
          </button>
        </div>
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

      <!-- Their columns: a number with the row's controls beside it, then
           the tag with its kind under it, address, traffic, latency, and the
           one-off check at the end. Egress and Country are what an Xray
           outbound reports about where it exits; ours have no equivalent, and
           a column that is always a dash is worse than no column. -->
      <div v-else class="table-wrap">
        <table>
          <thead>
            <tr>
              <th class="w-num">#</th>
              <th>{{ t('outbound.tag') }}</th>
              <th>{{ t('outbound.address') }}</th>
              <th class="w-md">{{ t('outbound.traffic') }}</th>
              <th class="w-sm">{{ t('outbound.latency') }}</th>
              <th class="w-sm right">{{ t('outbound.check') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(o, idx) in outbounds" :key="o.id" :class="{ off: !o.enabled }">
              <!-- The number, and beside it what can be done to the row --
                 which is how theirs puts edit and the menu inside the '#'
                 column rather than giving actions a column of their own. -->
              <td class="w-num">
                <div class="rownum">
                  <span class="num">{{ idx + 1 }}</span>
                  <button
                    class="act"
                    :title="t('action.edit')"
                    :disabled="isPending(o.id)"
                    @click="formFor = { outbound: o }"
                  >
                    <Icon name="edit" :size="15" />
                  </button>
                  <button
                    class="act"
                    :title="o.enabled ? t('action.disable') : t('action.enable')"
                    :disabled="o.builtin || isPending(o.id)"
                    @click="setEnabled(o, !o.enabled)"
                  >
                    <Icon :name="o.enabled ? 'pause' : 'play'" :size="15" />
                  </button>
                  <button
                    class="act danger"
                    :title="o.builtin ? t('outbound.builtinLocked') : t('action.delete')"
                    :disabled="o.builtin || isPending(o.id)"
                    @click="remove(o)"
                  >
                    <Icon name="trash" :size="15" />
                  </button>
                </div>
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
                <span v-if="o.txBytes || o.rxBytes" class="traffic">
                  <span class="up">↑ {{ bytes(o.txBytes) }}</span>
                  <span class="down">↓ {{ bytes(o.rxBytes) }}</span>
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
                  class="act round"
                  :title="t('outbound.checkOne')"
                  :disabled="isPending(o.id)"
                  @click="check(o)"
                >
                  <span v-if="isPending(o.id)" class="spin sm"></span>
                  <Icon v-else name="zap" :size="15" />
                </button>
              </td>
            </tr>
          </tbody>
        </table>
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
