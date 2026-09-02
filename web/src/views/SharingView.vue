<script setup>
import { computed, onMounted, onBeforeUnmount, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '../lib/api.js'
import { t, tn, notify } from '../lib/store.js'
import Icon from '../components/Icon.vue'
import ErrorState from '../components/ErrorState.vue'

const reports = ref([])
const loading = ref(true)
let timer = null

onMounted(() => {
  load()
  // The window this looks at is ten minutes wide, so a slow refresh is enough
  // and a fast one would only add load for no new information.
  timer = setInterval(load, 30_000)
})
onBeforeUnmount(() => clearInterval(timer))

const loadError = ref(null)

async function load() {
  try {
    reports.value = await api.get('/api/sharing')
    loadError.value = null
  } catch (e) {
    loadError.value = e
    notify(e.message, 'error')
  } finally {
    loading.value = false
  }
}

function since(iso) {
  if (!iso) return '—'
  const mins = Math.round((Date.now() - new Date(iso)) / 60000)
  if (mins < 60) return tn('sharing.minutesAgo', mins)
  const hours = Math.round(mins / 60)
  if (hours < 24) return tn('sharing.hoursAgo', hours)
  return tn('sharing.daysAgo', Math.round(hours / 24))
}

const total = computed(() => reports.value.length)
</script>

<template>
  <div class="page-head">
    <div>
      <h1>{{ t('nav.sharing') }}</h1>
      <p class="lede">{{ t('sharing.lede') }}</p>
    </div>
    <div class="page-actions">
      <button class="btn ghost" @click="load">
        <Icon name="refresh" :size="15" />
        <span>{{ t('common.refresh') }}</span>
      </button>
    </div>
  </div>

  <!-- Said plainly and up front, because acting on this without reading it
       means disconnecting paying customers who did nothing wrong. -->
  <div class="banner info stack" role="note">
    <div class="banner-head">
      <Icon name="info" :size="15" />
      <strong>{{ t('sharing.readFirst') }}</strong>
    </div>
    <p>{{ t('sharing.caveat') }}</p>
  </div>

  <p v-if="loading" class="muted">{{ t('common.loading') }}</p>

  <ErrorState v-else-if="loadError" :error="loadError" @retry="load" />

  <section v-else-if="!total" class="card empty-state">
    <Icon name="check" :size="22" />
    <p>{{ t('sharing.none') }}</p>
    <p class="muted">{{ t('sharing.noneHint') }}</p>
  </section>

  <section v-else class="card table-wrap">
    <table>
      <thead>
        <tr>
          <th>{{ t('sharing.client') }}</th>
          <th>{{ t('sharing.device') }}</th>
          <th>{{ t('sharing.addresses') }}</th>
          <th>{{ t('sharing.firstSeen') }}</th>
          <th class="right">{{ t('common.actions') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="r in reports" :key="r.accountId">
          <td>
            <RouterLink :to="`/clients/${r.clientId}`" class="cell-link">{{ r.clientName }}</RouterLink>
          </td>
          <td class="muted">{{ r.deviceName }}</td>
          <td>
            <div class="addr-list">
              <span v-for="a in r.addrs" :key="a" class="tag geekblue ltr">{{ a }}</span>
            </div>
          </td>
          <td class="muted ltr">{{ since(r.since) }}</td>
          <td class="right">
            <RouterLink :to="`/clients/${r.clientId}`" class="btn sm ghost">
              {{ t('sharing.review') }}
            </RouterLink>
          </td>
        </tr>
      </tbody>
    </table>
  </section>
</template>
