<script setup>
// The strip the overview closes with: uptime, the panel, and the server's
// addresses, hidden until asked for.
import { computed, ref } from 'vue'
import { store, t } from '../../lib/store.js'
import { bytes } from '../../lib/format.js'
import Icon from '../Icon.vue'

const props = defineProps({
  system: { type: Object, required: true },
  panel: { type: Object, default: null },
})
const sys = computed(() => props.system)
const showIp = ref(false)
const nf = (n) => Number(n || 0).toLocaleString(store.locale)

function duration(sec) {
  const s = Number(sec || 0)
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  const m = Math.floor((s % 3600) / 60)
  if (d) return `${d}d ${h}h`
  if (h) return `${h}h ${m}m`
  return `${m}m`
}

const ipv4 = computed(() => (sys.value?.ipv4 || [])[0] || '\u2014')
const ipv6 = computed(() => (sys.value?.ipv6 || [])[0] || '—')
</script>

<template>
  <article class="card ov-strip">
    <div class="ov-strip-grid">
      <div class="ov-strip-cell">
        <div class="ov-strip-head">
          <Icon name="clock" :size="15" /><span class="ov-kicker">{{ t('overview.uptime') }}</span>
        </div>
        <div class="ov-strip-pair">
          <div>
            <span class="ov-kicker">{{ t('overview.panelUptime') }}</span>
            <span class="ov-strip-value ltr">{{ duration(panel.uptimeSec) }}</span>
          </div>
          <div>
            <span class="ov-kicker">{{ t('overview.hostUptime') }}</span>
            <span class="ov-strip-value ltr">{{ duration(sys.host.uptimeSec) }}</span>
          </div>
        </div>
      </div>

      <div class="ov-strip-cell">
        <div class="ov-strip-head">
          <Icon name="database" :size="15" /><span class="ov-kicker">W-UI</span>
        </div>
        <div class="ov-strip-pair">
          <div>
            <span class="ov-kicker">RAM</span>
            <span class="ov-strip-value">{{ bytes(sys.panel.memoryBytes, store.locale) }}</span>
          </div>
          <div>
            <span class="ov-kicker">{{ t('overview.goroutines') }}</span>
            <span class="ov-strip-value">{{ nf(sys.panel.goroutines) }}</span>
          </div>
        </div>
      </div>

      <div class="ov-strip-cell">
        <div class="ov-strip-head">
          <Icon name="globe" :size="15" />
          <span class="ov-kicker">{{ t('overview.addresses') }}</span>
          <button
            class="ov-eye"
            :aria-label="t('overview.toggleAddresses')"
            :aria-pressed="showIp"
            @click="showIp = !showIp"
          >
            <Icon :name="showIp ? 'eyeOff' : 'eye'" :size="14" />
          </button>
        </div>
        <div class="ov-strip-pair ov-ip" :class="{ 'ip-hidden': !showIp }">
          <div>
            <span class="ov-kicker">IPv4</span>
            <span class="ov-strip-value ov-mono">{{ ipv4 }}</span>
          </div>
          <div>
            <span class="ov-kicker">IPv6</span>
            <span class="ov-strip-value ov-mono ov-ip-v6">{{ ipv6 }}</span>
          </div>
        </div>
      </div>
    </div>
  </article>
</template>

<style scoped>
/* ---------- strip ---------- */
.ov-strip-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 1px;
  background: var(--line-soft);
}
@media (max-width: 1100px) {
  .ov-strip-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
@media (max-width: 560px) {
  .ov-strip-grid { grid-template-columns: minmax(0, 1fr); }
}
.ov-strip-cell {
  background: var(--surface);
  padding: 16px 18px;
}
.ov-strip-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 14px;
}
.ov-strip-head svg {
  color: var(--muted);
}
.ov-strip-pair {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 14px;
}
.ov-strip-pair > div {
  display: flex;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
}
.ov-strip-value {
  font-size: var(--t-base);
  font-weight: 600;
  overflow-wrap: anywhere;
}
.ov-mono {
  font-family: var(--mono);
  font-size: var(--t-xs);
  direction: ltr;
  unicode-bidi: isolate;
}
.ov-ip.ip-hidden .ov-strip-value {
  /* Blurred rather than replaced: the shape stays, the value does not, which
     is what an overview shown on a shared screen needs. */
  filter: blur(5px);
  user-select: none;
}
.ov-eye {
  margin-inline-start: auto;
  width: 26px;
  height: 26px;
  display: grid;
  place-items: center;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--muted);
  cursor: pointer;
}
.ov-eye:hover {
  background: var(--surface-3);
  color: var(--ink);
}
</style>
