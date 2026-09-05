<script setup>
import { computed, onMounted, onBeforeUnmount, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../lib/api.js'
import { useDelayed } from '../lib/live.js'
import { t, notify } from '../lib/store.js'
import Icon from '../components/Icon.vue'

// What the panel is actually asking the machine to do.
//
// Everywhere else in the panel shows the panel's own account of the state. This
// shows the text itself, so an operator debugging a server is not reduced to
// trusting a summary written by the thing they are debugging.
const props = defineProps({ tab: { type: String, default: 'engine' } })
const router = useRouter()

const tabs = [
  { key: 'engine', icon: 'shield' },
  { key: 'templates', icon: 'code' },
  { key: 'logs', icon: 'info' },
]
const current = computed(() =>
  tabs.some((x) => x.key === props.tab) ? props.tab : 'engine',
)
function go(key) {
  router.push(`/configs/${key}`)
}

const configs = ref(null)
const logs = ref([])
const level = ref('')
const loading = ref(true)
const loadError = ref('')
const which = ref('enforcement')
const showWait = useDelayed(computed(() => loading.value && !configs.value))
let timer = null

const programs = computed(() => [
  { key: 'enforcement', text: configs.value?.enforcement || '' },
  { key: 'routing', text: configs.value?.routing || '' },
  { key: 'shaping', text: configs.value?.shaping || '' },
])
const shown = computed(() => programs.value.find((p) => p.key === which.value))
const health = computed(() => configs.value?.health || {})

async function loadConfigs(quiet = false) {
  if (!quiet) loading.value = true
  try {
    configs.value = await api.get('/api/configs', { background: quiet })
    loadError.value = ''
  } catch (err) {
    loadError.value = err.message
  } finally {
    loading.value = false
  }
}

async function loadLogs(quiet = false) {
  if (!quiet) loading.value = true
  try {
    const res = await api.get(
      `/api/logs?limit=300${level.value ? `&level=${level.value}` : ''}`,
      { background: quiet },
    )
    logs.value = res.entries || []
    loadError.value = ''
  } catch (err) {
    loadError.value = err.message
  } finally {
    loading.value = false
  }
}

async function refresh(quiet = false) {
  if (current.value === 'logs') await loadLogs(quiet)
  else await loadConfigs(quiet)
}

onMounted(() => {
  refresh()
  // Slow: this is a page somebody reads, not a dashboard. A program that
  // rewrote itself under the cursor would be unreadable.
  timer = setInterval(() => refresh(true), 20_000)
})
onBeforeUnmount(() => clearInterval(timer))

watch(current, () => refresh())
watch(level, () => loadLogs())

async function copy() {
  try {
    await navigator.clipboard.writeText(shown.value?.text || '')
    notify(t('configs.copied'), 'success')
  } catch {
    // Clipboard access is refused outside a secure context, which a panel on a
    // bare IP over http always is. Saying so beats a button that does nothing.
    notify(t('configs.copyRefused'), 'error')
  }
}
</script>

<template>
  <section class="view">
    <header class="page-head">
      <div>
        <h1>{{ t('nav.configs') }}</h1>
        <p class="muted">{{ t('configs.lede') }}</p>
      </div>
    </header>

    <div class="tabs" role="tablist">
      <button
        v-for="x in tabs"
        :key="x.key"
        role="tab"
        class="tab"
        :class="{ on: current === x.key }"
        :aria-selected="current === x.key"
        @click="go(x.key)"
      >
        <Icon :name="x.icon" :size="15" />
        {{ t(`nav.configs.${x.key}`) }}
      </button>
    </div>

    <div v-if="loadError" class="empty empty-cta">
      <Icon name="alert" :size="28" />
      <p>{{ loadError }}</p>
      <button class="btn" @click="refresh()">{{ t('action.retry') }}</button>
    </div>

    <!-- A spinner said work was happening and nothing about what was coming.
         These hold the shape of the page instead. -->
    <div v-else-if="showWait && current === 'engine'" class="engine-grid" aria-hidden="true">
      <div v-for="n in 4" :key="n" class="card sk-block">
        <span class="sk sk-lg" style="width: 42%"></span>
        <span class="sk" style="width: 70%"></span>
        <span class="sk" style="width: 54%"></span>
      </div>
    </div>
    <section v-else-if="showWait" class="card sk-block" aria-hidden="true">
      <span class="sk sk-lg" style="width: 30%"></span>
      <span class="sk sk-tall"></span>
    </section>
    <div v-else-if="loading" class="empty"></div>

    <!-- ── engine ──────────────────────────────────────────────────────── -->
    <div v-else-if="current === 'engine'" class="engine-grid">
      <div
        v-for="p in programs"
        :key="p.key"
        class="card engine-card"
        :class="{ inert: health[p.key] }"
      >
        <div class="card-head">
          <h2>{{ t(`configs.engine.${p.key}`) }}</h2>
          <span class="tag" :class="health[p.key] ? 'red' : 'green'">
            {{ health[p.key] ? t('configs.inactive') : t('configs.active') }}
          </span>
        </div>
        <div class="card-body">
          <p v-if="health[p.key]" class="muted">{{ health[p.key] }}</p>
          <p v-else class="muted">{{ t(`configs.engine.${p.key}.on`) }}</p>
          <p class="muted small">
            {{ p.text ? t('configs.lines').replace('{n}', p.text.split('\n').length) : t('configs.nothingYet') }}
          </p>
        </div>
      </div>
    </div>

    <!-- ── templates ───────────────────────────────────────────────────── -->
    <div v-else-if="current === 'templates'" class="card">
      <div class="toolbar">
        <div class="segmented" role="group" :aria-label="t('configs.which')">
          <button
            v-for="p in programs"
            :key="p.key"
            type="button"
            :class="{ on: which === p.key }"
            :aria-pressed="which === p.key"
            @click="which = p.key"
          >
            {{ t(`configs.engine.${p.key}`) }}
          </button>
        </div>
        <div class="spacer"></div>
        <button class="btn" :disabled="!shown?.text" @click="copy">
          <Icon name="copy" :size="16" /> {{ t('action.copy') }}
        </button>
      </div>

      <p class="notice info">{{ t('configs.readOnly') }}</p>

      <pre v-if="shown?.text" class="code ltr"><code>{{ shown.text }}</code></pre>
      <div v-else class="empty">
        <Icon name="code" :size="28" />
        <p class="muted">{{ t('configs.nothingYet') }}</p>
      </div>
    </div>

    <!-- ── logs ────────────────────────────────────────────────────────── -->
    <div v-else class="card">
      <div class="toolbar">
        <select v-model="level" :aria-label="t('configs.level')">
          <option value="">{{ t('configs.allLevels') }}</option>
          <option value="error">error</option>
          <option value="warn">warn</option>
          <option value="info">info</option>
        </select>
        <div class="spacer"></div>
        <button class="btn" @click="loadLogs()">
          <Icon name="refresh" :size="16" /> {{ t('action.refresh') }}
        </button>
      </div>

      <div v-if="!logs.length" class="empty">
        <Icon name="info" :size="28" />
        <p class="muted">{{ t('configs.noLogs') }}</p>
      </div>

      <ol v-else class="loglist">
        <li v-for="(e, i) in logs" :key="i" :class="e.level">
          <span class="log-time ltr">{{ e.time }}</span>
          <span class="tag" :class="{ red: e.level === 'error', orange: e.level === 'warn' }">
            {{ e.level }}
          </span>
          <span class="log-msg">{{ e.msg }}</span>
        </li>
      </ol>
    </div>
  </section>
</template>
