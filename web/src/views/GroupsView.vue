<script setup>
// Customer groups: the figures across the top, a row each, and what can be
// done to a group's members. How a row reads is lib/groups/rows.js; the
// table and the dialogs are their own; this holds the list and the actions
// that ask first.
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../lib/api.js'
import { useLive, useDelayed } from '../lib/live.js'
import ErrorState from '../components/ErrorState.vue'
import { store, t, tn, notify } from '../lib/store.js'
import { bytes } from '../lib/format.js'
import Icon from '../components/Icon.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import PageSpin from '../components/PageSpin.vue'
import GroupsTable from '../components/groups/GroupsTable.vue'
import GroupDialog from '../components/groups/GroupDialog.vue'
import MembersDialog from '../components/groups/MembersDialog.vue'

const router = useRouter()

const data = ref(null)
const loading = ref(true)
const dialog = ref(null) // { kind, group }
const members = ref(null) // { group, mode }
const busy = ref(false)

const nf = (n) => Number(n || 0).toLocaleString(store.locale)

const loadError = ref(null)

async function load(quiet = false) {
  if (!quiet) loading.value = true
  try {
    data.value = await api.groups({ background: quiet })
    loadError.value = null
  } catch (err) {
    // Saying "no groups yet, create one" to somebody whose request failed is
    // worse than saying nothing: it describes a state the panel cannot see.
    if (!quiet) {
      if (!data.value) loadError.value = err
      else notify(err.message, 'error')
    }
  } finally {
    loading.value = false
  }
}

const showSkeleton = useDelayed(computed(() => loading.value && !data.value))

onMounted(load)

// The totals here are sums over the customers, so they move whenever any of
// them does. Slower than the customer list itself: this is a summary, and
// nobody watches a summary for a change of one row.
useLive(load, { every: 10_000, busy: () => !!ask.value || !!dialog.value || !!members.value })

const items = computed(() => data.value?.items || [])
const totals = computed(() => data.value?.totals || {})

const ask = ref(null)

async function runConfirmed() {
  const spec = ask.value
  if (!spec) return
  busy.value = true
  try {
    await spec.run()
  } finally {
    busy.value = false
    ask.value = null
  }
}

// Deleting a group and deleting the people in it are different actions with
// very different consequences. Each dialog says which one this is, and the one
// that loses customers asks for the name to be typed.
function deleteGroup(g) {
  ask.value = {
    title: t('group.confirmDeleteTitle'),
    body: t('group.confirmDeleteBody'),
    subject: g.name,
    consequences: [tn('group.consequenceKept', g.clients)],
    confirmLabel: t('group.delete'),
    run: async () => {
      try {
        const res = await api.post('/api/groups/delete', { name: g.name })
        notify(tn('group.deleted', res.ungrouped ?? 0), 'success')
        await load()
      } catch (e) {
        notify(e.message, 'error')
      }
    },
  }
}

function pick(g, key) {
  if (key === 'addClients' || key === 'removeClients') {
    members.value = { group: g, mode: key === 'addClients' ? 'add' : 'remove' }
    return
  }
  if (key === 'rename' || key === 'extend' || key === 'quota') {
    dialog.value = { kind: key, group: g }
    return
  }
  // The two destructive ones ask before they run; the rest are reversible.
  if (key === 'delete') return deleteGroup(g)

  if (key === 'clear') {
    ask.value = {
      title: t('group.confirmDissolveTitle'),
      body: t('group.confirmDissolveBody'),
      subject: g.name,
      consequences: [tn('group.consequenceKept', g.clients)],
      confirmLabel: t('group.dissolve'),
      run: () => run({ action: key, group: g.name }),
    }
    return
  }

  if (key === 'deleteClients') {
    ask.value = {
      title: t('group.confirmDeleteClientsTitle'),
      body: t('group.confirmDeleteClientsBody'),
      subject: tn('client.nCustomers', g.clients),
      consequences: [t('client.consequenceConfigs'), t('client.consequenceUsage')],
      confirmLabel: t('action.delete'),
      // The single most destructive thing on this page: it removes paying
      // customers, not a label.
      requireText: g.name,
      run: () => run({ action: key, group: g.name }),
    }
    return
  }

  run({ action: key, group: g.name })
}

async function run(op) {
  busy.value = true
  try {
    const res = await api.groupAction(op)
    notify(`${t('group.applied')} — ${nf(res.affected)}`, 'success')
    await load()
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    busy.value = false
  }
}

async function done() {
  dialog.value = null
  members.value = null
  await load()
}

function viewMembers(g) {
  router.push({ path: '/clients', query: { group: g.name } })
}
</script>

<template>
  <!-- No page title and no lede. the classic panel opens this page straight on the
       figures, and the one control it has lives inside the table's card,
       where the thing it acts on is. -->

  <div class="strip card">
    <div class="strip-item">
      <span class="strip-label"><Icon name="tag" :size="14" />{{ t('group.total') }}</span>
      <span class="strip-value num">{{ nf(totals.groups) }}</span>
    </div>
    <div class="strip-item">
      <span class="strip-label"><Icon name="users" :size="14" />{{ t('group.grouped') }}</span>
      <span class="strip-value num">{{ nf(totals.groupedClients) }}</span>
    </div>
    <div class="strip-item">
      <span class="strip-label"><Icon name="swap" :size="14" />{{ t('group.upDown') }}</span>
      <span class="strip-value num ltr small">
        {{ bytes(totals.upBytes, store.locale) }} / {{ bytes(totals.downBytes, store.locale) }}
      </span>
    </div>
    <div class="strip-item">
      <span class="strip-label"><Icon name="swap" :size="14" />{{ t('client.traffic') }}</span>
      <span class="strip-value num ltr">{{ bytes(totals.usedBytes, store.locale) }}</span>
    </div>
  </div>

  <div class="card">
    <!-- Their Card title: a toolbar holding the primary button, top-left,
         above the table it adds to. It was next to the page heading here and
         again in the middle of the empty state -- two buttons for one action,
         neither of them where the table is. -->
    <div class="card-toolbar">
      <button class="btn primary" @click="dialog = { kind: 'create' }">
        <Icon name="plus" :size="14" />
        <span>{{ t('group.add') }}</span>
      </button>
    </div>

    <ErrorState v-if="loadError" :error="loadError" @retry="load()" />

    <PageSpin v-else-if="showSkeleton" />
    <div v-else-if="loading" class="empty"></div>

    <GroupsTable
      v-else
      :items="items"
      @pick="pick"
      @rename="(g) => (dialog = { kind: 'rename', group: g })"
      @view="viewMembers"
    />
  </div>

  <MembersDialog v-if="members" :group="members.group" :mode="members.mode" @close="members = null" @done="done" />
  <GroupDialog v-if="dialog" :kind="dialog.kind" :group="dialog.group || null" @close="dialog = null" @done="done" />

  <ConfirmDialog
    :open="!!ask"
    :title="ask?.title || ''"
    :body="ask?.body || ''"
    :subject="ask?.subject || ''"
    :consequences="ask?.consequences || []"
    :confirm-label="ask?.confirmLabel || ''"
    :require-text="ask?.requireText || ''"
    :busy="busy"
    @confirm="runConfirmed"
    @cancel="ask = null"
  />
</template>
