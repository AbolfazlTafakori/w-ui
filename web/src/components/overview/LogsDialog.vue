<script setup>
// The panel's recent log, from the overview, without leaving the page or
// opening an SSH session: searched, filtered, followed, copied and saved.
import { ref, watch, onMounted, onBeforeUnmount } from 'vue'
import { api, saveText } from '../../lib/api.js'
import { t, tn, notify } from '../../lib/store.js'
import Icon from '../Icon.vue'

const emit = defineEmits(['close'])

const logs = ref({ loading: true, entries: [], notice: '' })
const logLevel = ref('info')
const logLimit = ref(200)
const logQuery = ref('')
const logSource = ref('panel')
const logFollow = ref(false)
let logTimer = null

// Refreshing while the operator is reading is the point of following, so it has
// to be slow enough not to move the page under them and quick enough to be
// worth having on. Five seconds is what the classic panel settled on and it is right.
const followInterval = 5000

// Both of these used to live a page away. When something is wrong, the log and
// a fresh backup are the first two things wanted, and this is the page an
// operator is already looking at.
onMounted(loadLogs)

async function loadLogs() {
  if (!logs.value) return
  logs.value = { ...logs.value, loading: true }
  try {
    const params = new URLSearchParams({
      limit: String(logLimit.value),
      level: logLevel.value,
      source: logSource.value,
    })
    // Only when there is something to search for: an empty parameter would be
    // a needle that matches everything and is one more thing on the wire.
    if (logQuery.value.trim()) params.set('q', logQuery.value.trim())

    const res = await api.get(`/api/logs?${params}`)
    // The endpoint wraps its rows; taking the response itself would give an
    // object where a list is expected and render nothing at all.
    logs.value = {
      loading: false,
      entries: res?.entries || [],
      // A source this server cannot read is worth saying out loud rather than
      // showing an empty list, which reads as "nothing happened".
      notice: res?.notice || '',
    }
  } catch (e) {
    logs.value = { loading: false, entries: [], notice: e.message }
  }
}

// Searching on every keystroke would be a request per letter. Waiting for the
// typing to stop is one request for the word.
let searchTimer = null
function onLogSearch() {
  clearTimeout(searchTimer)
  searchTimer = setTimeout(loadLogs, 250)
}

watch(logFollow, (on) => {
  clearInterval(logTimer)
  if (on) logTimer = setInterval(loadLogs, followInterval)
})

// Following a log after its window has gone is a request every five seconds for
// nothing, forever.
watch(logs, (v) => {
  if (!v) {
    clearInterval(logTimer)
    logFollow.value = false
  }
})

onBeforeUnmount(() => {
  clearInterval(logTimer)
  clearTimeout(searchTimer)
})

// Always the same clock, in Latin digits, whatever language the panel is in.
//
// The old viewer formatted this for the locale, which in Persian produced
// ۰۵:۱۹:۲۰ — correct as a time and wrong as a log column: it does not line up
// with the entry beside it, does not match what is in the file on the server,
// and cannot be searched for. A log is machine output and reads the same to
// everybody.
function logStamp(value) {
  if (!value) return ''
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return ''
  const p = (n) => String(n).padStart(2, '0')
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

// What a line looks like as text, which is what gets copied and downloaded.
function logLine(e) {
  const at = e.time ? new Date(e.time).toISOString() : ''
  const fields = e.fields
    ? Object.entries(e.fields).map(([k, v]) => `${k}=${v}`).join(' ')
    : ''
  return [at, e.level, e.message, fields].filter(Boolean).join(' ')
}

function logText() {
  return (logs.value?.entries || []).map(logLine).join('\n')
}

async function copyLogs() {
  try {
    await navigator.clipboard.writeText(logText())
    notify(t('logs.copied'), 'ok')
  } catch {
    notify(t('action.copyFailed'), 'error')
  }
}

// Saved rather than shown, because the useful thing to do with a log is send it
// to somebody, and selecting a thousand lines in a scrolling box is not that.
function downloadLogs() {
  saveText(logText(), `wui-${new Date().toISOString().slice(0, 19).replace(/[:T]/g, '')}.log`)
}
</script>

<template>
  <!-- The recent log, without leaving the page or opening an SSH session. -->
  <div class="modal-backdrop" @click.self="emit('close')">
    <div class="modal logmodal" role="dialog" aria-modal="true" aria-labelledby="lg-title">
      <div class="card-head">
        <h2 id="lg-title">
          {{ t('settings.tab.logs') }}
          <span v-if="logs.loading" class="spin sm"></span>
          <span v-else class="muted small">{{ tn('logs.count', logs.entries.length) }}</span>
        </h2>
        <button class="btn sm icon ghost spacer" :aria-label="t('common.close')" @click="emit('close')">
          <Icon name="close" :size="15" />
        </button>
      </div>

      <!-- Everything an operator reaches for while reading a log, on one row,
           because each of these was previously a reason to leave the panel and
           open an SSH session. -->
      <div class="log-toolbar">
        <div class="log-search">
          <Icon name="search" :size="14" />
          <input
            v-model="logQuery"
            type="search"
            :placeholder="t('logs.searchHint')"
            :aria-label="t('logs.search')"
            @input="onLogSearch"
          />
        </div>

        <select v-model="logLevel" :aria-label="t('logs.level')" @change="loadLogs">
          <option value="debug">{{ t('logs.all') }}</option>
          <option value="info">{{ t('logs.info') }}</option>
          <option value="warn">{{ t('logs.warn') }}</option>
          <option value="error">{{ t('logs.error') }}</option>
        </select>

        <select v-model.number="logLimit" :aria-label="t('logs.rows')" class="ltr" @change="loadLogs">
          <option :value="50">50</option>
          <option :value="100">100</option>
          <option :value="200">200</option>
          <option :value="500">500</option>
          <option :value="1000">1000</option>
        </select>

        <!-- The buffer is this process's memory and is empty after a restart,
             which is usually the thing being asked about. The journal has it. -->
        <select v-model="logSource" :aria-label="t('logs.source')" @change="loadLogs">
          <option value="panel">{{ t('logs.sourcePanel') }}</option>
          <option value="journal">{{ t('logs.sourceJournal') }}</option>
        </select>

        <label class="log-follow">
          <input v-model="logFollow" type="checkbox" />
          <span>{{ t('logs.follow') }}</span>
        </label>

        <div class="spacer row">
          <button class="btn sm icon ghost" :aria-label="t('common.refresh')" @click="loadLogs">
            <Icon name="refresh" :size="15" />
          </button>
          <button class="btn sm icon ghost" :aria-label="t('action.copy')"
                  :disabled="!logs.entries.length" @click="copyLogs">
            <Icon name="copy" :size="15" />
          </button>
          <button class="btn sm icon ghost" :aria-label="t('logs.download')"
                  :disabled="!logs.entries.length" @click="downloadLogs">
            <Icon name="download" :size="15" />
          </button>
        </div>
      </div>

      <!-- Machine output, so it is laid out left to right whatever the page
           around it is doing. In a right-to-left interface the columns of a
           log line come out in the opposite order and the punctuation inside a
           timestamp or an address moves, which is unreadable and looks like a
           fault in the panel rather than in the text direction. -->
      <div class="card-body log-body ltr-block">
        <p v-if="logs.notice" class="log-notice">{{ logs.notice }}</p>
        <p v-if="logs.loading && !logs.entries.length" class="muted">{{ t('common.loading') }}</p>
        <p v-else-if="!logs.entries.length && !logs.notice" class="muted">
          {{ logQuery ? t('logs.noMatch') : t('logs.none') }}
        </p>
        <ol v-else class="loglist">
          <li v-for="(e, i) in logs.entries" :key="i" :class="'lvl-' + (e.level || '').toLowerCase()">
            <span class="log-time" :title="e.time">{{ logStamp(e.time) }}</span>
            <span class="log-level">{{ (e.level || '').toUpperCase() }}</span>
            <span class="log-text">
              <span class="log-msg">{{ e.message }}</span>
              <template v-if="e.fields">
                <span v-for="(v, k) in e.fields" :key="k" class="log-field">
                  <b>{{ k }}</b>={{ v }}
                </span>
              </template>
            </span>
          </li>
        </ol>
      </div>
    </div>
  </div>
</template>
