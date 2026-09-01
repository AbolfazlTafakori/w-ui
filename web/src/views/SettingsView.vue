<script setup>
import { ref, computed, onMounted } from 'vue'
import { api } from '../lib/api.js'
import { store, t, notify, loadMessages } from '../lib/store.js'
import Icon from '../components/Icon.vue'

const info = ref(null)
const loading = ref(true)

const pw = ref({ current: '', next: '', confirm: '' })
const pwBusy = ref(false)
const pwError = ref('')

onMounted(async () => {
  try {
    info.value = await api.system()
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    loading.value = false
  }
})

const uptime = computed(() => {
  const s = info.value?.uptimeSec ?? 0
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  const m = Math.floor((s % 3600) / 60)
  if (d) return `${d}d ${h}h`
  if (h) return `${h}h ${m}m`
  return `${m}m`
})

async function changePassword() {
  pwError.value = ''
  if (pw.value.next.length < 8) {
    pwError.value = t('settings.passwordTooShort')
    return
  }
  if (pw.value.next !== pw.value.confirm) {
    pwError.value = t('settings.passwordMismatch')
    return
  }

  pwBusy.value = true
  try {
    await api.changePassword(pw.value.current, pw.value.next)
    pw.value = { current: '', next: '', confirm: '' }
    notify(t('settings.passwordChanged'), 'success')
  } catch (err) {
    pwError.value = err.message
  } finally {
    pwBusy.value = false
  }
}

// Saving the language on the account rather than only in this browser means it
// follows the operator to their phone.
async function saveLocale(event) {
  const locale = event.target.value
  try {
    store.admin = await api.updateMe({ locale })
    await loadMessages(locale)
    notify(t('settings.saved'), 'success')
  } catch (err) {
    notify(err.message, 'error')
  }
}
</script>

<template>
  <div class="page-head">
    <div>
      <h1>{{ t('nav.settings') }}</h1>
      <p>{{ t('settings.subtitle') }}</p>
    </div>
  </div>

  <div v-if="loading" class="card"><div class="empty"><span class="spin"></span></div></div>

  <template v-else-if="info">
    <div class="cols">
      <section class="card">
        <div class="card-head">
          <Icon name="info" :size="17" />
          <h2>{{ t('settings.system') }}</h2>
        </div>
        <dl class="rows">
          <div><dt>{{ t('settings.version') }}</dt><dd class="mono">{{ info.version }}</dd></div>
          <div><dt>{{ t('settings.listen') }}</dt><dd class="mono">{{ info.listen }}</dd></div>
          <div><dt>{{ t('settings.platform') }}</dt><dd class="mono">{{ info.platform }}</dd></div>
          <div><dt>Go</dt><dd class="mono">{{ info.goVersion }}</dd></div>
          <div><dt>{{ t('settings.uptime') }}</dt><dd class="mono ltr">{{ uptime }}</dd></div>
          <div>
            <dt>{{ t('settings.protocols') }}</dt>
            <dd class="row" style="gap: 6px; justify-content: flex-end">
              <span v-for="p in info.protocols" :key="p" class="tag proto">{{ p }}</span>
            </dd>
          </div>
          <!-- The global banner announces that enforcement is off; this row is
               where an operator finds out why. -->
          <div>
            <dt>{{ t('settings.enforcement') }}</dt>
            <dd>
              <span class="tag" :class="info.enforcementActive ? 'active' : 'exhausted'">
                <i v-if="info.enforcementActive" class="dot"></i>
                {{ info.enforcementActive ? t('enforcement.active') : t('status.disabled') }}
              </span>
              <div v-if="info.enforcementMessage" class="mono muted" style="font-size: var(--t-xs); margin-top: 4px">
                {{ info.enforcementMessage }}
              </div>
            </dd>
          </div>
        </dl>
      </section>

      <section class="card">
        <div class="card-head">
          <Icon name="database" :size="17" />
          <h2>{{ t('settings.storage') }}</h2>
        </div>
        <dl class="rows">
          <div><dt>{{ t('settings.dbDriver') }}</dt><dd class="mono">{{ info.dbDriver }}</dd></div>
          <div>
            <dt>{{ t('nav.interfaces') }}</dt>
            <dd class="num">{{ info.interfaces.toLocaleString(store.locale) }}</dd>
          </div>
          <div>
            <dt>{{ t('nav.clients') }}</dt>
            <dd class="num">{{ info.clients.toLocaleString(store.locale) }}</dd>
          </div>
          <div>
            <dt>{{ t('device.title') }}</dt>
            <dd class="num">{{ info.accounts.toLocaleString(store.locale) }}</dd>
          </div>
        </dl>
      </section>

      <section class="card">
        <div class="card-head">
          <Icon name="globe" :size="17" />
          <h2>{{ t('settings.preferences') }}</h2>
        </div>
        <div class="card-body">
          <div class="field">
            <label for="set-locale">{{ t('settings.language') }}</label>
            <select id="set-locale" :value="store.locale" @change="saveLocale">
              <option v-for="l in info.locales" :key="l" :value="l">
                {{ l === 'fa' ? 'فارسی' : 'English' }}
              </option>
            </select>
            <span class="hint">{{ t('settings.languageHint') }}</span>
          </div>
        </div>
      </section>

      <section class="card">
        <div class="card-head">
          <Icon name="key" :size="17" />
          <h2>{{ t('settings.changePassword') }}</h2>
        </div>
        <form class="card-body form" @submit.prevent="changePassword">
          <div class="field">
            <label for="pw-cur">{{ t('settings.currentPassword') }}</label>
            <input
              id="pw-cur"
              v-model="pw.current"
              type="password"
              autocomplete="current-password"
              required
            />
          </div>
          <div class="field">
            <label for="pw-new">{{ t('settings.newPassword') }}</label>
            <input
              id="pw-new"
              v-model="pw.next"
              type="password"
              autocomplete="new-password"
              minlength="8"
              required
            />
            <span class="hint">{{ t('settings.passwordHint') }}</span>
          </div>
          <div class="field">
            <label for="pw-conf">{{ t('settings.confirmPassword') }}</label>
            <input
              id="pw-conf"
              v-model="pw.confirm"
              type="password"
              autocomplete="new-password"
              required
            />
          </div>

          <p v-if="pwError" class="error" role="alert">{{ pwError }}</p>

          <div class="row" style="justify-content: flex-end">
            <button class="btn primary" type="submit" :disabled="pwBusy">
              <span v-if="pwBusy" class="spin"></span>
              <template v-else>{{ t('settings.updatePassword') }}</template>
            </button>
          </div>
        </form>
      </section>
    </div>
  </template>
</template>

<style scoped>
.cols {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
  gap: 16px;
  align-items: start;
}
.rows {
  margin: 0;
  padding: 6px 0;
}
.rows > div {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 11px 18px;
  border-bottom: 1px solid var(--line-soft);
}
.rows > div:last-child {
  border-bottom: none;
}
.rows dt {
  color: var(--muted);
  font-size: var(--t-sm);
  flex-shrink: 0;
}
.rows dd {
  margin: 0 0 0 auto;
  margin-inline-start: auto;
  color: var(--ink);
  text-align: end;
  overflow-wrap: anywhere;
}
.form {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.error {
  margin: 0;
  color: var(--bad);
  font-size: var(--t-sm);
  background: var(--bad-soft);
  border: 1px solid rgba(244, 101, 95, 0.32);
  border-radius: var(--radius-sm);
  padding: 10px 13px;
}
.card-head svg {
  color: var(--muted);
}
</style>
