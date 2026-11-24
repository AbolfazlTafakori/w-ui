<script setup>
// What an admin's tree changed -- their own and their resellers' -- or a
// reseller's own, when the owner granted them the page; for the owner, every
// operator's. The action as it was routed and how it ended, never what was
// sent: there is no password, token or link here to show.
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { api } from '../lib/api.js'
import { dateTime } from '../lib/format.js'
import { store, t, notify } from '../lib/store.js'
import AntIcon from '../components/AntIcon.vue'

// Embedded on the owner's settings/security page: the record alone, without
// the page's own summary and frame, which the settings card already gives it.
defineProps({ embedded: { type: Boolean, default: false } })

const rows = ref([])
const loading = ref(true)
const search = ref('')
let timer = null

async function load(quiet = false) {
  try {
    rows.value = await api.get('/api/operator/activity?limit=500', { background: quiet })
  } catch (e) {
    if (!quiet) notify(e.message, 'error')
  } finally {
    loading.value = false
  }
}
onMounted(() => {
  load()
  // A record of what was done: a minute is soon enough to see the next.
  timer = setInterval(() => load(true), 60_000)
})
onBeforeUnmount(() => clearInterval(timer))

// Who made a change: their name, or -- for an operator removed since, whose
// record outlives them -- that they were removed, by the id they had.
const who = (r) => r.operator || t('activity.removedOperator').replace('{id}', r.operatorId || '?')

// How a request ended, the way the table colours it.
const outcome = (s) => (s < 400 ? 'done' : s < 500 ? 'refused' : 'failed')
const outcomeLabel = (s) => ({
  done: t('activity.filter.done'), refused: t('activity.filter.refused'), failed: t('activity.filter.failed'),
})[outcome(s)]
const result = ref('')
const visible = computed(() => {
  const q = search.value.trim().toLowerCase()
  return rows.value.filter((r) =>
    (!result.value || outcome(r.status) === result.value) &&
    (!q || [who(r), r.action].some((v) => String(v || '').toLowerCase().includes(q))))
})
const operators = computed(() => new Set(rows.value.map((r) => r.operatorId || r.operator)).size)
const notDone = computed(() => rows.value.filter((r) => outcome(r.status) !== 'done').length)
// Done, refused, failed: green, amber, and the panel's own warning tone for a
// fault -- nothing here is an alarm.
const tone = (s) => (s < 400 ? 'green' : s < 500 ? 'orange' : 'gold')
</script>

<template>
  <div :class="embedded ? 'activity-embedded' : 'antpage activity'">
    <!-- The summary Card: how much was done, by how many, how much did not go through. -->
    <div v-if="!embedded" class="acard small summary-card">
      <div class="acard-body">
        <div class="arow">
          <div class="acol">
            <div class="stat-title">{{ t('activity.stat.records') }}</div>
            <div class="stat-content ltr"><span class="stat-prefix"><AntIcon name="ProfileOutlined" /></span><span>{{ rows.length }}</span></div>
          </div>
          <div class="acol">
            <div class="stat-title">{{ t('activity.stat.operators') }}</div>
            <div class="stat-content ltr"><span class="stat-prefix"><AntIcon name="TeamOutlined" /></span><span>{{ operators }}</span></div>
          </div>
          <div class="acol">
            <div class="stat-title">{{ t('activity.stat.refused') }}</div>
            <div class="stat-content ltr"><span class="stat-prefix"><AntIcon name="StopOutlined" /></span><span>{{ notDone }}</span></div>
          </div>
        </div>
      </div>
    </div>

    <!-- The list Card: refresh, search and the result filter over the table. -->
    <div :class="embedded ? '' : 'acard'">
      <div :class="embedded ? 'activity-toolbar' : 'acard-head'">
        <div class="aspace">
          <button class="abtn primary" @click="load()"><AntIcon name="ReloadOutlined" /><span>{{ t('action.refresh') }}</span></button>
          <label class="ainput">
            <span class="ainput-prefix"><AntIcon name="SearchOutlined" /></span>
            <input v-model="search" type="text" :placeholder="t('iface.menu.search')" :aria-label="t('iface.menu.search')" />
            <button v-if="search" type="button" class="ainput-clear" :aria-label="t('action.cancel')" @click="search = ''"><AntIcon name="CloseCircleFilled" /></button>
          </label>
          <div class="aselect"><select v-model="result" :aria-label="t('activity.result')">
            <option value="">{{ t('activity.filter.all') }}</option>
            <option value="done">{{ t('activity.filter.done') }}</option>
            <option value="refused">{{ t('activity.filter.refused') }}</option>
            <option value="failed">{{ t('activity.filter.failed') }}</option>
          </select></div>
        </div>
      </div>
      <div :class="embedded ? '' : 'acard-body'">
        <div class="aalert info with-desc" role="note">
          <AntIcon name="InfoCircleFilled" />
          <div class="aalert-body"><span class="aalert-desc">{{ t('activity.about') }}</span></div>
        </div>
        <div v-if="!visible.length && !loading" class="aempty"><AntIcon name="InboxOutlined" />{{ t('activity.empty') }}</div>
        <div v-else-if="visible.length" class="atable-wrap">
          <table class="atable small" style="min-width: 640px">
            <thead>
              <tr>
                <th>{{ t('activity.when') }}</th>
                <th>{{ t('activity.who') }}</th>
                <th>{{ t('activity.what') }}</th>
                <th>{{ t('activity.result') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="r in visible" :key="r.id">
                <td class="ltr act-muted">{{ dateTime(r.at, store.locale) }}</td>
                <td><span class="act-who" :class="{ gone: !r.operator }">{{ who(r) }}</span><span v-if="r.viaToken" class="atag" style="margin-inline-start: 6px"><AntIcon name="KeyOutlined" /> {{ t('activity.token') }}</span></td>
                <td><code class="ltr">{{ r.action }}</code></td>
                <td><span class="atag" :class="tone(r.status)">{{ r.status }} · {{ outcomeLabel(r.status) }}</span></td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.activity-toolbar { margin-bottom: 12px; }
.act-who { font-weight: 600; }
.act-who.gone { font-weight: 400; font-style: italic; color: var(--muted); }
.act-muted { color: var(--muted); white-space: nowrap; }
</style>
