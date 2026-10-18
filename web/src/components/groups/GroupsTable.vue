<script setup>
// The groups as a table. The header row stays when there is nothing under
// it, the way their Table keeps its columns and puts the empty state inside
// the body: a table that vanishes when empty makes the page a different
// shape depending on the data.
import { store, t } from '../../lib/store.js'
import { bytes } from '../../lib/format.js'
import { menuFor } from '../../lib/groups/rows.js'
import Icon from '../Icon.vue'
import MoreMenu from '../MoreMenu.vue'

defineProps({ items: { type: Array, required: true } })
// pick(g, key): a row menu choice; view(g): its customers.
defineEmits(['pick', 'rename', 'view'])

const nf = (n) => Number(n || 0).toLocaleString(store.locale)
</script>

<template>
  <div class="table-wrap groups-table">
    <table>
      <thead>
        <tr>
          <th class="w-gact">{{ t('table.actions') }}</th>
          <th>{{ t('group.name') }}</th>
          <th class="w-gcount">{{ t('group.clientCount') }}</th>
          <th class="w-gsize">{{ t('overview.upload') }}</th>
          <th class="w-gsize">{{ t('overview.download') }}</th>
          <th class="w-gtraffic">{{ t('group.trafficUsed') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-if="!items.length" class="empty-row">
          <td colspan="6">
            <div class="card-empty">
              <Icon name="tag" :size="32" />
              <div>{{ t('common.nothingYet') }}</div>
            </div>
          </td>
        </tr>
        <tr v-for="g in items" :key="g.name">
          <!-- Two controls, as theirs has: the whole menu behind one
               button, and rename lifted out of it because it is the one an
               operator reaches for most. -->
          <td class="w-gact">
            <div class="actions">
              <MoreMenu :items="menuFor(g)" compact button-class="act" :label="t('table.actions')" @pick="(key) => $emit('pick', g, key)" />
              <button class="act" :title="t('group.rename')" @click="$emit('rename', g)">
                <Icon name="edit" :size="16" />
              </button>
            </div>
          </td>

          <td>
            <button class="tag geekblue name" @click="$emit('view', g)">{{ g.name }}</button>
            <div v-if="g.note" class="sub muted small">{{ g.note }}</div>
          </td>

          <td class="num">{{ nf(g.clients) }}</td>
          <td class="muted"><span class="num ltr">{{ bytes(g.upBytes, store.locale) }}</span></td>
          <td class="muted"><span class="num ltr">{{ bytes(g.downBytes, store.locale) }}</span></td>
          <td><span class="num ltr">{{ bytes(g.usedBytes, store.locale) }}</span></td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style>
/* Keyed on the table rather than scoped: the menu's button is the shared
   menu's own element, and is styled as the rename button beside it. */
.groups-table .act {
  display: grid;
  place-items: center;
  width: 30px;
  height: 30px;
  border: none;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--muted);
  cursor: pointer;
}
.groups-table .act:hover {
  background: var(--surface-3);
  color: var(--ink);
}
.groups-table .name {
  border: 1px solid var(--tag-geek-line);
  cursor: pointer;
  font: inherit;
  font-family: var(--mono);
  font-size: var(--t-xs);
  font-weight: 600;
}
.groups-table .name:hover {
  background: var(--tag-geek-line);
  color: var(--tag-geek-ink);
}
</style>
