<script setup>
// A reseller's own account, over their customers: how many they may still
// add, how much traffic and how many days they have left, and why they are
// held back when they are.
import { computed } from 'vue'
import { store, t } from '../../lib/store.js'
import { bytes, dateTime } from '../../lib/format.js'
import { DEPLETING_AT } from '../../lib/clientRow.js'
import { cardOf } from '../../lib/capacity.js'
import AntIcon from '../AntIcon.vue'

const props = defineProps({ account: { type: Object, default: null } })
const nf = (n) => Number(n || 0).toLocaleString(store.locale)

const accountStrip = computed(() => {
  const a = props.account
  if (!a) return null
  const DAY = 86400e3
  const left = a.quotaBytes ? Math.max(0, a.quotaBytes - a.usedBytes) : null
  const spentPct = a.quotaBytes ? Math.min(100, Math.round((a.usedBytes / a.quotaBytes) * 100)) : null
  const days = a.expiresAt ? Math.ceil((new Date(a.expiresAt).getTime() - Date.now()) / DAY) : null
  let reason = ''
  if (!a.enabled) reason = t('admins.switchedOff')
  else if (days !== null && days <= 0) reason = t('admins.termEnded')
  else if (a.quotaBytes && a.usedBytes >= a.quotaBytes) reason = t('admins.allowanceSpent')
  // Users as the capacity card says them: seats held of what was given
  // (open), units left to spend (consumable), or a count with no ceiling.
  const cap = cardOf(a)
  let customers = `${nf(a.users)} / ∞`
  let customersColor = 'green'
  if (cap.users?.mode === 'consumable') {
    customers = t('cap.unitsLeft', { n: cap.users.free })
    customersColor = cap.users.free <= 0 ? 'orange' : 'green'
  } else if (cap.users) {
    customers = t('cap.usersOf', { used: cap.users.used + cap.users.handed, total: cap.users.given })
    customersColor = cap.users.free <= 0 ? 'orange' : 'green'
  }
  return {
    customers,
    customersColor,
    tokens: cap.tokens ? t('cap.tokensOf', { used: cap.tokens.used, total: cap.tokens.given }) : '',
    traffic: left === null ? '∞' : bytes(left, store.locale),
    trafficColor: left === null ? 'purple' : spentPct >= 100 ? 'red' : spentPct >= DEPLETING_AT ? 'orange' : 'green',
    time: days === null ? '∞' : days <= 0 ? t('admins.termEnded') : t('admins.daysLeft', { n: nf(days) }),
    timeColor: days === null ? 'purple' : days <= 0 ? 'red' : days <= 7 ? 'orange' : 'green',
    timeTitle: a.expiresAt ? dateTime(a.expiresAt, store.locale) : '',
    reason,
  }
})
</script>

<template>
  <div v-if="accountStrip" class="acard small account-card">
    <div class="acard-body">
      <div class="account-row">
        <span class="account-title"><AntIcon name="ShopOutlined" /> {{ t('admins.you.title') }}</span>
        <span class="account-item">{{ t('admins.you.customers') }} <span class="atag" dir="auto" :class="accountStrip.customersColor">{{ accountStrip.customers }}</span></span>
        <span v-if="accountStrip.tokens" class="account-item">{{ t('nav.tokens') }} <span class="atag blue" dir="auto">{{ accountStrip.tokens }}</span></span>
        <span class="account-item">{{ t('admins.you.trafficLeft') }} <span class="atag ltr" :class="accountStrip.trafficColor">{{ accountStrip.traffic }}</span></span>
        <span class="account-item">{{ t('admins.you.timeLeft') }} <span class="atag" dir="auto" :class="accountStrip.timeColor" :title="accountStrip.timeTitle">{{ accountStrip.time }}</span></span>
      </div>
      <p v-if="accountStrip.reason" class="account-barred">{{ t('admins.you.barred', { reason: accountStrip.reason }) }}</p>
    </div>
  </div>
</template>

<style scoped>
.account-card { margin-bottom: 16px; }
.account-row { display: flex; align-items: center; flex-wrap: wrap; gap: 8px 20px; }
.account-title { display: inline-flex; align-items: center; gap: 6px; font-weight: 600; color: var(--ink); }
.account-item { display: inline-flex; align-items: center; gap: 6px; color: var(--muted); font-size: 13px; }
.account-barred { margin: 8px 0 0; color: var(--bad); font-size: 13px; }
</style>
