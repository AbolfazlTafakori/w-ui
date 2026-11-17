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

const visible = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return rows.value
  return rows.value.filter((r) => [r.operator, r.action].some((v) => String(v || '').toLowerCase().includes(q)))
})
// Done, refused, failed: green, amber, and the panel's own warning tone for a
// fault -- nothing here is an alarm.
const tone = (s) => (s < 400 ? 'green' : s < 500 ? 'orange' : 'gold')
</script>

<template>
  <div class="antpage activity">
    <div class="acard small">
      <div class="acard-head">
        <div class="card-toolbar">
          <button class="abtn primary" @click="load()"><AntIcon name="ReloadOutlined" /><span>{{ t('action.refresh') }}</span></button>
          <label class="ainput" style="max-width: 320px; width: 100%">
            <span class="ainput-prefix"><AntIcon name="SearchOutlined" /></span>
            <input v-model="search" type="text" :placeholder="t('iface.menu.search')" :aria-label="t('iface.menu.search')" />
          </label>
        </div>
      </div>
      <div class="acard-body">
        <div class="aalert info with-desc" role="note">
          <AntIcon name="InfoCircleFilled" />
          <div class="aalert-body"><span class="aalert-desc">{{ t('activity.about') }}</span></div>
        </div>
        <div v-if="!visible.length && !loading" class="aempty"><AntIcon name="InboxOutlined" />{{ t('activity.empty') }}</div>
        <div v-else class="atable-wrap">
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
                <td class="ltr">{{ dateTime(r.at, store.locale) }}</td>
                <td>{{ r.operator }}<span v-if="r.viaToken" class="atag" style="margin-inline-start: 6px">{{ t('activity.token') }}</span></td>
                <td><code class="ltr">{{ r.action }}</code></td>
                <td><span class="atag" :class="tone(r.status)">{{ r.status }}</span></td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>
  </div>
</template>
