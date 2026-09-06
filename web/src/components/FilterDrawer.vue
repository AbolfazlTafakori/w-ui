<script setup>
import { computed } from 'vue'
import Icon from './Icon.vue'
import MultiSelect from './MultiSelect.vue'
import { t } from '../lib/store.js'

// The filter panel, laid out the way 3x-ui lays its own out: a 420px drawer
// from the side, sections in the same order, and a footer with Clear all on
// one end and Done on the other.
//
// Their Telegram-user-id section is not here. It filters on a field a customer
// in this panel does not have, and a control that can only ever return
// everything is worse than no control.

const props = defineProps({
  open: { type: Boolean, default: false },
  modelValue: { type: Object, required: true },
  interfaces: { type: Array, default: () => [] },
  groups: { type: Array, default: () => [] },
})
const emit = defineEmits(['update:modelValue', 'close'])

const BUCKETS = ['active', 'depleting', 'exhausted', 'expired', 'disabled', 'online']

function patch(key, value) {
  emit('update:modelValue', { ...props.modelValue, [key]: value })
}

function toggleBucket(key, on) {
  const next = new Set(props.modelValue.buckets || [])
  on ? next.add(key) : next.delete(key)
  patch('buckets', [...next])
}

const serverOptions = computed(() =>
  props.interfaces.map((i) => ({
    value: i.id,
    label: i.name,
    tags: [{ text: t(`protocol.${i.protocol}`), kind: 'proto' }],
  })),
)

const groupOptions = computed(() => props.groups.map((g) => ({ value: g, label: g })))

const protocolOptions = [
  { value: 'wireguard', label: t('protocol.wireguard') },
  { value: 'openvpn', label: t('protocol.openvpn') },
]

// Three-state rows: All, then the two answers. Their Radio.Group with
// optionType="button" is a segmented control, so this is one.
const TRISTATE = [
  { value: '', key: 'common.all' },
  { value: 'on', key: 'settings.on' },
  { value: 'off', key: 'settings.off' },
]
const HAS = [
  { value: '', key: 'common.all' },
  { value: 'yes', key: 'filter.has' },
  { value: 'no', key: 'filter.hasNot' },
]

// emptyFilters comes from the plain <script> block below, which shares this
// scope: an export is not allowed inside <script setup>, and the page needs
// the same definition so both agree on what "no filter" means.
function clearAll() {
  emit('update:modelValue', emptyFilters())
}
</script>

<script>
// Exported separately so the page can build an empty filter without importing
// the component's internals, and so the two agree on what "empty" means.
export function emptyFilters() {
  return {
    buckets: [],
    protocols: [],
    interfaceIds: [],
    groups: [],
    expiryFrom: '',
    expiryTo: '',
    usedFromGB: '',
    usedToGB: '',
    renews: '',
    hasNote: '',
  }
}

// How many categories are narrowing the list. Shown on the button, so an
// operator who left a filter on last week can see that they did.
export function activeFilterCount(f) {
  if (!f) return 0
  let n = 0
  if (f.buckets?.length) n++
  if (f.protocols?.length) n++
  if (f.interfaceIds?.length) n++
  if (f.groups?.length) n++
  if (f.expiryFrom || f.expiryTo) n++
  if (f.usedFromGB !== '' || f.usedToGB !== '') n++
  if (f.renews) n++
  if (f.hasNote) n++
  return n
}
</script>

<template>
  <Transition name="drawer">
    <div v-if="open" class="fd-scrim" @click.self="emit('close')">
      <aside class="fd" role="dialog" aria-modal="true" aria-labelledby="fd-title">
        <header class="fd-head">
          <button class="btn sm icon ghost" :aria-label="t('common.close')" @click="emit('close')">
            <Icon name="close" :size="15" />
          </button>
          <h2 id="fd-title">{{ t('filter.title') }}</h2>
        </header>

        <div class="fd-body">
          <div class="fd-block">
            <span class="fd-label">{{ t('filter.status') }}</span>
            <div class="fd-checks">
              <label v-for="b in BUCKETS" :key="b" class="check">
                <input
                  type="checkbox"
                  :checked="(modelValue.buckets || []).includes(b)"
                  @change="toggleBucket(b, $event.target.checked)"
                />
                <span>{{ t(`filter.bucket.${b}`) }}</span>
              </label>
            </div>
          </div>

          <div class="fd-block">
            <span class="fd-label">{{ t('client.protocol') }}</span>
            <MultiSelect
              :model-value="modelValue.protocols"
              :options="protocolOptions"
              :placeholder="t('client.protocol')"
              @update:model-value="patch('protocols', $event)"
            />
          </div>

          <div class="fd-block">
            <span class="fd-label">{{ t('nav.interfaces') }}</span>
            <MultiSelect
              :model-value="modelValue.interfaceIds"
              :options="serverOptions"
              :placeholder="t('client.selectServers')"
              @update:model-value="patch('interfaceIds', $event)"
            />
          </div>

          <div class="fd-block">
            <span class="fd-label">{{ t('client.group') }}</span>
            <MultiSelect
              :model-value="modelValue.groups"
              :options="groupOptions"
              :placeholder="t('client.groupPlaceholder')"
              @update:model-value="patch('groups', $event)"
            />
          </div>

          <div class="fd-block">
            <span class="fd-label">{{ t('client.expires') }}</span>
            <div class="fd-pair">
              <input
                type="date"
                class="ltr"
                :value="modelValue.expiryFrom"
                :aria-label="t('filter.from')"
                @input="patch('expiryFrom', $event.target.value)"
              />
              <span class="fd-arrow">→</span>
              <input
                type="date"
                class="ltr"
                :value="modelValue.expiryTo"
                :aria-label="t('filter.to')"
                @input="patch('expiryTo', $event.target.value)"
              />
            </div>
          </div>

          <div class="fd-block">
            <span class="fd-label">{{ t('client.traffic') }} (GB)</span>
            <div class="fd-pair">
              <input
                type="number"
                min="0"
                step="1"
                class="ltr"
                :placeholder="t('filter.from')"
                :value="modelValue.usedFromGB"
                @input="patch('usedFromGB', $event.target.value)"
              />
              <input
                type="number"
                min="0"
                step="1"
                class="ltr"
                :placeholder="t('filter.to')"
                :value="modelValue.usedToGB"
                @input="patch('usedToGB', $event.target.value)"
              />
            </div>
          </div>

          <div class="fd-block">
            <span class="fd-label">{{ t('client.resetCycle') }}</span>
            <div class="seg" role="group">
              <button
                v-for="o in TRISTATE"
                :key="o.value"
                type="button"
                class="seg-btn"
                :class="{ on: (modelValue.renews || '') === o.value }"
                @click="patch('renews', o.value)"
              >
                {{ t(o.key) }}
              </button>
            </div>
          </div>

          <div class="fd-block">
            <span class="fd-label">{{ t('client.note') }}</span>
            <div class="seg" role="group">
              <button
                v-for="o in HAS"
                :key="o.value"
                type="button"
                class="seg-btn"
                :class="{ on: (modelValue.hasNote || '') === o.value }"
                @click="patch('hasNote', o.value)"
              >
                {{ t(o.key) }}
              </button>
            </div>
          </div>
        </div>

        <footer class="fd-foot">
          <button type="button" class="btn danger-ghost" @click="clearAll">
            {{ t('filter.clearAll') }}
          </button>
          <button type="button" class="btn primary" @click="emit('close')">
            {{ t('filter.done') }}
          </button>
        </footer>
      </aside>
    </div>
  </Transition>
</template>

<style scoped>
.fd-scrim {
  position: fixed;
  inset: 0;
  z-index: 60;
  display: flex;
  justify-content: flex-end;
  background: rgba(0, 0, 0, 0.45);
}
/* 420, the width their Drawer is given. */
.fd {
  display: flex;
  flex-direction: column;
  width: 420px;
  max-width: 100vw;
  background: var(--surface);
  border-inline-start: 1px solid var(--line);
  box-shadow: var(--elev-3);
}
.fd-head {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 16px 18px;
  border-bottom: 1px solid var(--line-soft);
}
.fd-head h2 {
  margin: 0;
  font-size: var(--t-md);
}
.fd-body {
  flex: 1;
  overflow-y: auto;
  padding: 18px;
}
.fd-block {
  margin-bottom: 22px;
}
.fd-block:last-child {
  margin-bottom: 0;
}
/* Their label is Typography.Text strong above the control. */
.fd-label {
  display: block;
  margin-bottom: 8px;
  font-size: var(--t-base);
  font-weight: 600;
  color: var(--ink);
}
.fd-checks {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.fd-pair {
  display: flex;
  align-items: center;
  gap: 8px;
}
.fd-pair > input {
  flex: 1;
  min-width: 0;
}
.fd-arrow {
  flex: none;
  color: var(--faint);
}

/* Their Radio.Group with optionType="button": one joined control, the chosen
   segment filled rather than outlined. */
.seg {
  display: inline-flex;
  border: 1px solid var(--line);
  border-radius: var(--radius-sm);
  overflow: hidden;
}
.seg-btn {
  padding: 8px 16px;
  border: 0;
  border-inline-end: 1px solid var(--line);
  background: var(--surface-2);
  color: var(--ink-2);
  font: inherit;
  font-size: var(--t-sm);
  cursor: pointer;
  transition: background-color 0.14s var(--ease), color 0.14s var(--ease);
}
.seg-btn:last-child {
  border-inline-end: 0;
}
.seg-btn:hover:not(.on) {
  background: var(--surface-3);
}
.seg-btn.on {
  background: var(--accent);
  color: var(--accent-ink);
}

.fd-foot {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  padding: 14px 18px;
  border-top: 1px solid var(--line-soft);
}

.drawer-enter-active .fd,
.drawer-leave-active .fd {
  transition: transform 0.22s var(--ease-enter);
}
.drawer-enter-active,
.drawer-leave-active {
  transition: opacity 0.18s var(--ease);
}
.drawer-enter-from,
.drawer-leave-to {
  opacity: 0;
}
.drawer-enter-from .fd,
.drawer-leave-to .fd {
  transform: translateX(100%);
}
[dir='rtl'] .drawer-enter-from .fd,
[dir='rtl'] .drawer-leave-to .fd {
  transform: translateX(-100%);
}

@media (prefers-reduced-motion: reduce) {
  .drawer-enter-active .fd,
  .drawer-leave-active .fd {
    transition: none;
  }
  .drawer-enter-from .fd,
  .drawer-leave-to .fd {
    transform: none;
  }
}
</style>
