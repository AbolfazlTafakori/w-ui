<script setup>
import { computed, onMounted, ref } from 'vue'
import { api } from '../lib/api.js'
import { useDelayed } from '../lib/live.js'
import { t, notify } from '../lib/store.js'
import Icon from '../components/Icon.vue'
import ErrorState from '../components/ErrorState.vue'

const docs = ref(null)
const loading = ref(true)
const showWait = useDelayed(computed(() => loading.value))
const query = ref('')
const openGroup = ref(null)
const copied = ref('')

const loadError = ref(null)

async function load() {
  loading.value = true
  try {
    docs.value = await api.get('/api/docs')
    openGroup.value = docs.value.groups?.[0]?.name ?? null
    loadError.value = null
  } catch (e) {
    loadError.value = e
    notify(e.message, 'error')
  } finally {
    loading.value = false
  }
}

onMounted(load)

// Searching an API reference is how it is actually used: you know roughly what
// you want to do, not which heading it lives under. A match anywhere opens its
// group, so a hit is never hidden behind a collapsed section.
const groups = computed(() => {
  if (!docs.value) return []
  const q = query.value.trim().toLowerCase()
  if (!q) return docs.value.groups

  return docs.value.groups
    .map((g) => ({
      ...g,
      routes: g.routes.filter((r) =>
        `${r.method} ${r.path} ${r.summary} ${r.note || ''}`.toLowerCase().includes(q),
      ),
    }))
    .filter((g) => g.routes.length)
})

const matchCount = computed(() =>
  groups.value.reduce((n, g) => n + g.routes.length, 0),
)

function isOpen(name) {
  // While searching everything is open: hiding a result behind a heading is
  // the same as not finding it.
  return !!query.value.trim() || openGroup.value === name
}

function toggle(name) {
  openGroup.value = openGroup.value === name ? null : name
}

// A command someone can paste, with this server's own address already in it.
function curlFor(route) {
  const url = `${docs.value.baseUrl}${route.path}`
  const lines = [`curl -X ${route.method} '${url}'`]
  if (route.auth) lines.push(`  -H 'Authorization: Bearer $TOKEN'`)
  if (route.body) {
    lines.push(`  -H 'Content-Type: application/json'`)
    lines.push(`  -d '${route.body}'`)
  }
  return lines.join(' \\\n')
}

async function copy(text, id) {
  try {
    await navigator.clipboard.writeText(text)
    copied.value = id
    setTimeout(() => (copied.value = ''), 1600)
  } catch {
    notify(t('api.copyFailed'), 'error')
  }
}

const methodTone = (m) =>
  ({ GET: 'green', POST: 'blue', PUT: 'orange', PATCH: 'orange', DELETE: 'red' })[m] || 'grey'
</script>

<template>
  <div class="page-head">
    <div>
      <h1>{{ t('nav.api') }}</h1>
      <p class="lede">{{ t('api.lede') }}</p>
    </div>
  </div>

  <!-- Roughly what arrives: the authentication note, the search box, then the
       routes grouped under their headings. -->
  <template v-if="showWait">
    <section class="card sk-block" aria-hidden="true">
      <span class="sk sk-lg" style="width: 34%"></span>
      <span class="sk" style="width: 76%"></span>
      <span class="sk sk-tall"></span>
    </section>
    <section class="card sk-block" aria-hidden="true">
      <span class="sk sk-lg"></span>
    </section>
    <section v-for="g in 3" :key="g" class="card sk-block" aria-hidden="true">
      <span class="sk sk-lg" style="width: 22%"></span>
      <span v-for="n in 4" :key="n" class="sk" :style="{ width: 88 - n * 9 + '%' }"></span>
    </section>
  </template>
  <div v-else-if="loading" class="empty"></div>

  <ErrorState v-else-if="loadError" :error="loadError" @retry="load" />

  <template v-else-if="docs">
    <!-- How to authenticate, first: nothing else here works without it. -->
    <section class="card api-intro">
      <div class="api-intro-head">
        <Icon name="key" :size="15" />
        <strong>{{ t('api.authTitle') }}</strong>
      </div>
      <p>{{ docs.auth }}</p>
      <pre class="api-code ltr"><code>{{ `curl -X POST '${docs.baseUrl}/api/auth/login' \\
  -H 'Content-Type: application/json' \\
  -d '{"username":"admin","password":"…"}'` }}</code></pre>
    </section>

    <section class="card api-search">
      <span class="control">
        <Icon name="search" :size="15" />
        <input v-model="query" type="search" :placeholder="t('api.search')" />
      </span>
      <span class="muted small ltr">{{ matchCount }}</span>
    </section>

    <p v-if="query && !matchCount" class="muted">{{ t('api.noMatches') }}</p>

    <section v-for="g in groups" :key="g.name" class="card api-group">
      <button class="api-group-head" :aria-expanded="isOpen(g.name)" @click="toggle(g.name)">
        <Icon name="chevronDown" :size="15" :class="{ turned: !isOpen(g.name) }" />
        <span class="api-group-name">{{ g.name }}</span>
        <span class="muted small ltr">{{ g.routes.length }}</span>
      </button>

      <ul v-if="isOpen(g.name)" class="api-routes">
        <li v-for="r in g.routes" :key="r.method + r.path" class="api-route">
          <div class="api-route-head">
            <span class="tag ltr" :class="methodTone(r.method)">{{ r.method }}</span>
            <code class="api-path ltr">{{ r.path }}</code>
            <span v-if="!r.auth" class="tag grey">{{ t('api.noToken') }}</span>
            <button
              class="linkbtn"
              :title="t('api.copyCurl')"
              @click="copy(curlFor(r), r.method + r.path)"
            >
              {{ copied === r.method + r.path ? t('api.copied') : t('api.copy') }}
            </button>
          </div>

          <p class="api-summary">{{ r.summary }}</p>
          <p v-if="r.note" class="api-note">
            <Icon name="info" :size="13" />
            <span>{{ r.note }}</span>
          </p>
          <pre v-if="r.body" class="api-code ltr"><code>{{ r.body }}</code></pre>
        </li>
      </ul>
    </section>
  </template>
</template>
