<script setup>
// The nodes as a table, in the classic panel's column order. Enabled is the
// second column, a switch beside the actions; the readings run left to right
// from status to heartbeat. The header row stays when there is nothing
// under it.
import { store, t } from '../../lib/store.js'
import { bytes } from '../../lib/format.js'
import { ago, latencyTone, takesImport, uptime } from '../../lib/nodes/rows.js'
import Icon from '../Icon.vue'
import AntIcon from '../AntIcon.vue'

defineProps({
  nodes: { type: Array, required: true },
  pending: { type: Set, required: true },
})
defineEmits(['probe', 'update', 'import', 'edit', 'remove', 'toggle'])
</script>

<template>
  <div class="table-wrap">
    <table>
      <thead>
        <tr>
          <th class="w-gact">{{ t('table.actions') }}</th>
          <th class="w-sm">{{ t('table.enabled') }}</th>
          <th>{{ t('node.name') }}</th>
          <th>{{ t('node.address') }}</th>
          <th class="w-md">{{ t('node.status') }}</th>
          <th class="w-sm">{{ t('node.cpu') }}</th>
          <th class="w-sm">{{ t('node.mem') }}</th>
          <th class="w-md">{{ t('node.version') }}</th>
          <th class="w-md">{{ t('node.uptime') }}</th>
          <th class="w-md">{{ t('node.transfer') }}</th>
          <th class="w-sm">{{ t('node.latency') }}</th>
          <th class="w-md">{{ t('node.lastSeen') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-if="!nodes.length" class="empty-row">
          <td colspan="12">
            <div class="card-empty">
              <Icon name="hdd" :size="32" />
              <div>{{ t('common.nothingYet') }}</div>
            </div>
          </td>
        </tr>
        <tr v-for="n in nodes" :key="n.id">
          <td class="w-gact">
            <div class="actions">
              <button class="act" :title="t('node.probe')" :disabled="n.kind === 'local' || pending.has(n.id)" @click="$emit('probe', n)">
                <span v-if="pending.has(n.id)" class="spin sm"></span>
                <Icon v-else name="refresh" :size="16" />
              </button>
              <!-- No binary travels from here. The node fetches the release
                   itself and checks the signature, so taking this panel does
                   not mean running code of your choosing on every node. -->
              <button class="act" :title="t('update.askNode')" :disabled="n.kind === 'local' || pending.has(n.id)" @click="$emit('update', n)">
                <Icon name="download" :size="16" />
              </button>
              <button v-if="takesImport(n)" class="act" :title="t('nodeImport.action')" @click="$emit('import', n)">
                <AntIcon name="ImportOutlined" />
              </button>
              <button class="act" :title="t('action.edit')" :disabled="n.kind === 'local'" @click="$emit('edit', n)">
                <Icon name="edit" :size="16" />
              </button>
              <button class="act danger" :title="t('action.delete')" :disabled="n.kind === 'local'" @click="$emit('remove', n)">
                <Icon name="trash" :size="16" />
              </button>
            </div>
          </td>

          <td>
            <input
              type="checkbox"
              :checked="n.enabled"
              :disabled="n.kind === 'local' || pending.has(n.id)"
              :aria-label="n.name"
              @change="$emit('toggle', n, $event.target.checked)"
            />
          </td>

          <td>
            <span class="nodename">{{ n.name }}</span>
            <!-- The panel's own row is marked, because everything else on it
                 behaves differently and an operator should not wonder why. -->
            <span v-if="n.kind === 'local'" class="tag grey">{{ t('node.thisPanel') }}</span>
            <span v-else-if="n.kind === 'xui'" class="tag blue">3x-ui</span>
            <span v-else-if="n.kind === 'pasarguard'" class="tag blue">Pasarguard</span>
            <!-- Shown only when it is not 1. An operator looking at a customer
                 whose usage climbed faster than their traffic should be able to
                 see the reason here rather than open every node in turn. -->
            <span v-if="n.usageCoefficient && n.usageCoefficient !== 1" class="tag num ltr"
                  :title="t('node.coefficientHint')">x{{ n.usageCoefficient }}</span>
            <!-- Said on the row, because a node nobody is checking the identity
                 of is a decision somebody made once and forgot. -->
            <span v-if="n.kind !== 'local' && n.tlsMode === 'skip'" class="tag red"
                  :title="t('node.tlsSkipWarning')">{{ t('node.tlsUnchecked') }}</span>
            <span v-else-if="n.kind !== 'local' && n.tlsMode === 'mtls'" class="tag green"
                  :title="t('node.tlsMutualHint')">{{ t('node.tlsMutualShort') }}</span>
            <span v-else-if="n.kind !== 'local' && n.tlsMode === 'pin'" class="tag grey"
                  :title="t('node.tlsPinnedHint')">{{ t('node.tlsPinned') }}</span>
            <!-- Said plainly, because an operator whose customers have quietly
                 moved off a server deserves to know it was this and not a
                 fault. -->
            <span v-if="n.overAllowance" class="tag red" :title="t('node.spentHint')">
              {{ t('node.spent') }}
            </span>
            <div v-if="n.note" class="sub muted small">{{ n.note }}</div>
          </td>

          <td class="muted small ltr">{{ n.address || '—' }}</td>

          <td>
            <span v-if="n.kind === 'local'" class="tag green"><i class="dot"></i>{{ t('node.running') }}</span>
            <span v-else-if="!n.enabled" class="tag grey">{{ t('status.disabled') }}</span>
            <span v-else-if="n.reachable" class="tag green"><i class="dot"></i>{{ t('node.online') }}</span>
            <!-- The reason, not just the colour: "refused" and "no route" send
                 an operator to entirely different places. -->
            <span v-else class="tag red" :title="n.lastError">{{ t('node.offline') }}</span>
          </td>

          <td class="num ltr">{{ n.cpuPercent ? n.cpuPercent.toFixed(0) + '%' : '—' }}</td>
          <td class="num ltr">{{ n.memPercent ? n.memPercent.toFixed(0) + '%' : '—' }}</td>
          <td class="muted small ltr">{{ n.version || '—' }}</td>
          <td class="muted small ltr">{{ uptime(n.uptimeSec) }}</td>
          <td class="muted small ltr">
            <template v-if="n.dataLimitBytes">
              {{ bytes(n.usedBytes || 0, store.locale) }} / {{ bytes(n.dataLimitBytes, store.locale) }}
            </template>
            <template v-else>{{ bytes(n.usedBytes || 0, store.locale) }}</template>
          </td>

          <td>
            <span v-if="n.kind === 'local'" class="muted">—</span>
            <span v-else class="tag num ltr" :class="latencyTone(n.latencyMs)">{{ n.latencyMs || 0 }} ms</span>
          </td>

          <td class="muted small">{{ n.kind === 'local' ? t('node.justNow') : ago(n.lastSeenAt) }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
.nodename {
  font-weight: 600;
  margin-inline-end: 6px;
}
</style>
