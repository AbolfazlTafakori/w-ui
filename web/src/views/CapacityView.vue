<script setup>
// An admin's or a reseller's capacity: how many users and tokens they have,
// and every movement of them -- each customer made, extended, ended or
// removed, each charge from above, each hand-down to a reseller. Their own
// account's lines, an admin's resellers' too; never another tree's (the
// server narrows it, whatever this page asks for).
import { computed, onMounted, ref } from 'vue'
import { api } from '../lib/api.js'
import { cardOf } from '../lib/capacity.js'
import { dateTime } from '../lib/format.js'
import { store, t, notify } from '../lib/store.js'
import AntIcon from '../components/AntIcon.vue'

const PAGE = 100
// Every label by its own key, so the check that each exists in every
// language can see them.
const MODE = { open: 'cap.mode.open', consumable: 'cap.mode.consumable' }
const MODE_HINT = { open: 'cap.mode.openHint', consumable: 'cap.mode.consumableHint' }
const KIND = { '': 'cap.kind.all', users: 'cap.kind.users', tokens: 'cap.kind.tokens' }
const REASON = {
  create: 'cap.reason.create', extend: 'cap.reason.extend', grow: 'cap.reason.grow', shrink: 'cap.reason.shrink',
  release: 'cap.reason.release', ended: 'cap.reason.ended', revived: 'cap.reason.revived', charge: 'cap.reason.charge',
  debit: 'cap.reason.debit', handdown: 'cap.reason.handdown', handback: 'cap.reason.handback', mode: 'cap.reason.mode',
  issue: 'cap.reason.issue', migrate: 'cap.reason.migrate',
}
const rows = ref([])
const kind = ref('')
const loading = ref(true)
const more = ref(false)

const nf = (n) => Number(n || 0).toLocaleString(store.locale)
const card = computed(() => cardOf(store.admin))

async function load(append = false) {
  loading.value = true
  try {
    const before = append && rows.value.length ? rows.value[rows.value.length - 1].id : 0
    const q = new URLSearchParams({ limit: String(PAGE) })
    if (kind.value) q.set('kind', kind.value)
    if (before) q.set('before', String(before))
    const got = (await api.get('/api/operator/ledger?' + q.toString())).items || []
    rows.value = append ? rows.value.concat(got) : got
    more.value = got.length === PAGE
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    loading.value = false
  }
}
onMounted(() => load())

function pick(k) {
  kind.value = k
  load()
}

// What a line did, in a word: the reason, and which figure moved.
// A hand-down is two lines, one on each account: handed out of the giver's
// (the handed figure), received into the reseller's (granted). Each is said
// from the side of the account it is on.
const what = (r) => {
  if (r.reason === 'handdown' && r.field === 'granted') return t('cap.reason.received')
  if (r.reason === 'handback' && r.field === 'granted') return t('cap.reason.returned')
  return REASON[r.reason] ? t(REASON[r.reason]) : r.reason
}
// Units as the operator reads them: what they gained is +, what went out
// of their free count is -. Granted up is +; used or handed down is -.
function units(r) {
  const d = Number(r.delta) || 0
  const toFree = r.field === 'granted' ? d : -d
  return (toFree > 0 ? '+' : toFree < 0 ? '−' : '') + nf(Math.abs(toFree))
}
const tone = (r) => {
  const d = Number(r.delta) || 0
  const toFree = r.field === 'granted' ? d : -d
  return toFree > 0 ? 'green' : toFree < 0 ? 'blue' : ''
}
</script>

<template>
  <div class="antpage capacity">
    <div class="cap-cards">
      <div v-if="card.users" class="acard small cap-card">
        <div class="acard-body">
          <div class="cap-head"><AntIcon name="TeamOutlined" /> {{ t('cap.users') }}
            <span class="atag" :class="card.users.mode === 'consumable' ? 'purple' : 'blue'">{{ t(MODE[card.users.mode] || MODE.open) }}</span>
          </div>
          <div class="cap-figure" dir="auto">
            <template v-if="card.users.mode === 'consumable'">{{ t('cap.unitsLeft', { n: card.users.free }) }}</template>
            <template v-else>{{ t('cap.usersOf', { used: card.users.used + card.users.handed, total: card.users.given }) }}</template>
          </div>
          <div class="cap-bar" role="progressbar" :aria-valuenow="Math.round(card.users.fill * 100)" aria-valuemin="0" aria-valuemax="100">
            <span :style="{ width: card.users.fill * 100 + '%' }" :class="{ full: card.users.free <= 0 }"></span>
          </div>
          <p class="cap-note">{{ t(MODE_HINT[card.users.mode] || MODE_HINT.open) }}</p>
          <p v-if="card.users.handed" class="cap-note">{{ t('cap.handedDown', { n: card.users.handed }) }}</p>
        </div>
      </div>
      <div v-if="card.tokens" class="acard small cap-card">
        <div class="acard-body">
          <div class="cap-head"><AntIcon name="KeyOutlined" /> {{ t('nav.tokens') }}</div>
          <div class="cap-figure" dir="auto">{{ t('cap.tokensOf', { used: card.tokens.used, total: card.tokens.given }) }}</div>
          <div class="cap-bar" role="progressbar" :aria-valuenow="Math.round(card.tokens.fill * 100)" aria-valuemin="0" aria-valuemax="100">
            <span :style="{ width: card.tokens.fill * 100 + '%' }" :class="{ full: card.tokens.used >= card.tokens.given }"></span>
          </div>
          <p class="cap-note">{{ t('cap.tokensHint') }}</p>
        </div>
      </div>
    </div>

    <div class="acard small">
      <div class="acard-head">
        <div class="card-toolbar">
          <span class="cap-title">{{ t('cap.history') }}</span>
          <div class="aradio-group" role="radiogroup" :aria-label="t('cap.history')">
            <button v-for="k in ['', 'users', 'tokens']" :key="k" type="button" class="aradio-btn" :class="{ active: kind === k }" role="radio" :aria-checked="kind === k" @click="pick(k)">
              {{ t(KIND[k]) }}
            </button>
          </div>
        </div>
      </div>
      <div class="acard-body">
        <div v-if="!rows.length && !loading" class="aempty"><AntIcon name="InboxOutlined" />{{ t('cap.empty') }}</div>
        <div v-else class="atable-wrap">
          <table class="atable small" style="min-width: 640px">
            <thead>
              <tr>
                <th>{{ t('activity.when') }}</th>
                <th>{{ t('cap.account') }}</th>
                <th>{{ t('cap.what') }}</th>
                <th>{{ t('cap.units') }}</th>
                <th>{{ t('cap.for') }}</th>
                <th>{{ t('activity.who') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="r in rows" :key="r.id">
                <td class="ltr">{{ dateTime(r.at, store.locale) }}</td>
                <td>{{ r.operator }} <span class="atag">{{ t(KIND[r.kind] || KIND['']) }}</span></td>
                <td>{{ what(r) }}</td>
                <td><span class="atag ltr" :class="tone(r)">{{ units(r) }}</span></td>
                <td>{{ r.client || (r.tokenId ? '#' + r.tokenId : '') }}</td>
                <td>{{ r.actor || t('cap.byPanel') }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <div v-if="more" class="cap-more">
          <button class="abtn" type="button" :disabled="loading" @click="load(true)">{{ t('cap.older') }}</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.cap-cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(260px, 1fr)); gap: 16px; margin-bottom: 16px; }
.cap-head { display: flex; align-items: center; gap: 8px; color: var(--muted); font-size: 13px; }
.cap-figure { margin: 8px 0 10px; font-size: 22px; font-weight: 600; color: var(--ink); }
.cap-bar { height: 6px; border-radius: 3px; background: var(--surface-2); overflow: hidden; }
.cap-bar span { display: block; height: 100%; background: var(--tag-blue-ink); transition: width 0.3s; }
.cap-bar span.full { background: var(--warn); }
.cap-note { margin: 8px 0 0; color: var(--muted); font-size: 12px; }
.cap-title { font-weight: 600; color: var(--ink); margin-inline-end: auto; }
.cap-more { display: flex; justify-content: center; margin-top: 12px; }
</style>
