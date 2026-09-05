<script setup>
import { ref, computed, onMounted } from 'vue'
import { useRouter, RouterLink } from 'vue-router'
import { makeQR } from '../lib/qr.js'
import { api, apiURL, getToken } from '../lib/api.js'
import { store, t, tn, notify } from '../lib/store.js'
import { bytes, dateTime, relative, percent, isOnline } from '../lib/format.js'
import Icon from '../components/Icon.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'

const props = defineProps({ id: { type: [String, Number], required: true } })
const router = useRouter()

const client = ref(null)
const loading = ref(true)
const profile = ref(null)
const qr = ref('')
// The size the code must be shown at, which comes from the code: see lib/qr.
const qrSize = ref(340)
const showConfig = ref(false)
const newDevice = ref('')
const busy = ref(false)
const downloading = ref(false)

// Devices, not accounts. A customer on three servers holds three accounts
// per device, and showing the row count against the device limit would read
// as 6 / 2 for somebody who has two devices.
const deviceCount = computed(
  () => new Set((client.value?.accounts || []).map((a) => a.deviceName.toLowerCase())).size,
)

// Which servers this customer reaches, for the same reason: the device
// table lists one row per account, and the count above it must not.
const serverCount = computed(
  () => new Set((client.value?.accounts || []).map((a) => a.interfaceId)).size,
)

// Every device's configuration in one archive.
//
// Fetched with the session token rather than linked: these are private keys,
// and a plain link carries no Authorization header — it would either fail or
// force the panel to serve them unauthenticated.
async function downloadAll() {
  downloading.value = true
  try {
    const res = await fetch(apiURL(`/api/clients/${props.id}/configs`), {
      headers: { Authorization: `Bearer ${getToken()}` },
    })
    if (!res.ok) throw new Error((await res.text()) || t('error.unknown'))

    // The server names the file after the customer; falling back to the id
    // beats saving something called "download".
    const disposition = res.headers.get('Content-Disposition') || ''
    const named = /filename="([^"]+)"/.exec(disposition)
    const url = URL.createObjectURL(await res.blob())
    const a = document.createElement('a')
    a.href = url
    a.download = named ? named[1] : `client-${props.id}.zip`
    a.click()
    URL.revokeObjectURL(url)
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    downloading.value = false
  }
}

const savingProfile = ref(false)

async function saveProfile() {
  savingProfile.value = true
  try {
    await api.downloadProfile(profile.value.account.id)
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    savingProfile.value = false
  }
}

async function load() {
  loading.value = true
  try {
    client.value = await api.client(props.id)
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    loading.value = false
  }
}

onMounted(load)

async function showProfile(account) {
  try {
    const p = await api.profile(account.id)
    profile.value = { ...p, account }
    qr.value = ''
    // Opening another device is not asking to see its key.
    showConfig.value = false
    // WireGuard clients import a tunnel by camera, so the QR is the primary
    // delivery path on mobile. OpenVPN clients cannot, so it is skipped there.
    if (client.value.protocol === 'wireguard') {
      const code = await makeQR(p.body)
      qr.value = code.dataUrl
      qrSize.value = code.size
    }
  } catch (err) {
    notify(err.message, 'error')
  }
}

async function guard(fn, successKey) {
  try {
    const result = await fn()
    if (successKey) notify(t(successKey), 'success')
    return result
  } catch (err) {
    notify(err.message, 'error')
  }
}

const addDevice = () =>
  guard(async () => {
    await api.addDevice(props.id, newDevice.value)
    newDevice.value = ''
    await load()
  }, 'device.added')

const ask = ref(null)

const removeDevice = (account) => {
  ask.value = {
    title: t('device.confirmRemoveTitle'),
    body: t('device.confirmRemoveBody'),
    subject: account.deviceName,
    // Both worth saying: the customer's file stops working, and the address it
    // held goes back to the pool and may be handed to somebody else.
    consequences: [t('device.consequenceConfig'), t('device.consequenceAddress')],
    confirmLabel: t('action.delete'),
    run: () =>
      guard(async () => {
        await api.removeDevice(account.id)
        await load()
      }, 'device.removed'),
  }
}

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

const setStatus = (status) =>
  guard(async () => {
    client.value = await api.updateClient(props.id, { status })
  }, 'client.updated')

const resetTraffic = () =>
  guard(async () => {
    client.value = await api.resetTraffic(props.id)
  }, 'client.trafficReset')

const remove = () => {
  const c = client.value
  ask.value = {
    title: t('client.confirmDeleteTitle'),
    body: t('client.confirmDeleteBody'),
    subject: c?.name || '',
    consequences: [
      tn('client.consequenceDevices', deviceCount.value),
      t('client.consequenceConfigs'),
      t('client.consequenceUsage'),
    ],
    confirmLabel: t('action.delete'),
    run: () =>
      guard(async () => {
        await api.deleteClient(props.id)
        router.push('/clients')
      }, 'client.deleted'),
  }
}

async function copy(text) {
  try {
    await navigator.clipboard.writeText(text)
    notify(t('action.copied'), 'success')
  } catch {
    notify(t('action.copyFailed'), 'error')
  }
}
</script>

<template>
  <div v-if="loading" class="card"><div class="empty"><span class="spin"></span></div></div>

  <template v-else-if="client">
    <div class="page-head">
      <div>
        <RouterLink to="/clients" class="back small muted">
          ‹ {{ t('nav.clients') }}
        </RouterLink>
        <h1>{{ client.name }}</h1>
        <p class="row">
          <span class="tag proto">{{ client.protocol }}</span>
          <span class="tag" :class="client.status">
            <i v-if="client.status === 'active'" class="dot"></i>
            {{ t(`status.${client.status}`) }}
          </span>
          <span v-if="client.note" class="muted">{{ client.note }}</span>
        </p>
      </div>
      <div class="spacer row">
        <button v-if="client.status === 'active'" class="btn sm" @click="setStatus('disabled')">
          <Icon name="power" :size="13" />{{ t('action.disable') }}
        </button>
        <button v-else class="btn sm" @click="setStatus('active')">
          <Icon name="power" :size="13" />{{ t('action.enable') }}
        </button>
        <button class="btn sm" @click="resetTraffic">
          <Icon name="refresh" :size="13" />{{ t('action.resetTraffic') }}
        </button>
        <button class="btn sm danger" @click="remove">
          <Icon name="trash" :size="13" />{{ t('action.delete') }}
        </button>
      </div>
    </div>

    <div class="card" style="margin-bottom: 16px">
      <dl class="metrics">
        <div class="metric">
          <dt>{{ t('client.used') }}</dt>
          <dd>{{ bytes(client.usedBytes, store.locale) }}</dd>
          <!-- Shown only once there is a split to show. The enforcer that
               cannot tell the directions apart leaves both at zero, and
               "↓ 0 B ↑ 0 B" under a real total reads as a fault. -->
          <dd v-if="client.downBytes || client.upBytes" class="muted small num ltr">
            ↓ {{ bytes(client.downBytes, store.locale) }}
            ↑ {{ bytes(client.upBytes, store.locale) }}
          </dd>
        </div>
        <div class="metric">
          <dt>{{ t('client.quota') }}</dt>
          <dd>{{ client.quotaBytes ? bytes(client.quotaBytes, store.locale) : t('client.unlimited') }}</dd>
        </div>
        <div class="metric">
          <dt>{{ t('client.expires') }}</dt>
          <!-- A plan waiting for its first connection has no date yet. Saying
               "never expires" here would be the opposite of the truth about a
               plan with a fixed length. -->
          <dd v-if="!client.expiresAt && client.startOnFirstUse && client.durationDays > 0">
            <span class="tag blue ltr">{{ client.durationDays }}d</span>
            <span class="muted small">{{ t('client.notStartedHint') }}</span>
          </dd>
          <dd v-else>
            {{ client.expiresAt ? relative(client.expiresAt, store.locale) : t('client.neverExpires') }}
          </dd>
        </div>
        <div class="metric">
          <dt>{{ t('client.deviceLimit') }}</dt>
          <dd class="ltr">
            {{ deviceCount }} / {{ client.deviceLimit }}
            <span v-if="serverCount > 1" class="muted small">
              · {{ tn('client.acrossServers', serverCount) }}
            </span>
          </dd>
        </div>
        <div class="metric">
          <dt>{{ t('client.resetCycle') }}</dt>
          <dd>{{ t(`reset.${client.resetCycle}`) }}</dd>
        </div>
      </dl>
      <div v-if="client.quotaBytes" class="quota-bar">
        <div class="meter">
          <span
            :class="percent(client.usedBytes, client.quotaBytes) >= 95 ? 'bad' : percent(client.usedBytes, client.quotaBytes) >= 80 ? 'warn' : ''"
            :style="{ width: (percent(client.usedBytes, client.quotaBytes) ?? 0) + '%' }"
          ></span>
        </div>
      </div>
    </div>

    <div class="card">
      <div class="card-head">
        <h2>{{ t('device.title') }}</h2>
        <div class="spacer row">
          <input
            v-model="newDevice"
            :placeholder="t('device.namePlaceholder')"
            style="max-width: 170px"
            @keyup.enter="addDevice"
          />
          <button
            class="btn sm primary"
            :disabled="deviceCount >= client.deviceLimit"
            :title="deviceCount >= client.deviceLimit ? t('error.deviceLimitReached') : ''"
            @click="addDevice"
          >
            <Icon name="plus" :size="13" />{{ t('device.add') }}
          </button>
          <!-- A .conf holds one [Interface], so several devices cannot be one
               file. Shown only when there is more than one to collect. -->
          <button
            v-if="(client.accounts?.length ?? 0) > 1"
            class="btn sm"
            :title="t('device.downloadAllHint')"
            :disabled="downloading"
            @click="downloadAll"
          >
            <span v-if="downloading" class="spin sm"></span>
            <Icon v-else name="download" :size="13" />{{ t('device.downloadAll') }}
          </button>
        </div>
      </div>

      <div v-if="!client.accounts?.length" class="empty">{{ t('device.none') }}</div>

      <div v-else class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>{{ t('device.name') }}</th>
              <th>{{ t('device.address') }}</th>
              <th>{{ t('client.status') }}</th>
              <th>{{ t('device.lastSeen') }}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="acc in client.accounts" :key="acc.id">
              <td>{{ acc.deviceName }}</td>
              <td class="mono small">{{ acc.ip }}</td>
              <td>
                <span class="tag" :class="isOnline(acc.lastHandshake) ? 'active' : 'disabled'">
                  <i v-if="isOnline(acc.lastHandshake)" class="dot"></i>
                  {{ isOnline(acc.lastHandshake) ? t('status.online') : t('status.offline') }}
                </span>
              </td>
              <td class="small muted">{{ dateTime(acc.lastHandshake, store.locale) }}</td>
              <td>
                <div class="row" style="justify-content: flex-end; flex-wrap: nowrap">
                  <button class="btn sm" @click="showProfile(acc)">
                    <Icon name="qr" :size="13" />{{ t('device.config') }}
                  </button>
                  <button
                    class="btn sm icon danger"
                    :aria-label="t('action.delete')"
                    @click="removeDevice(acc)"
                  >
                    <Icon name="trash" :size="13" />
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <div v-if="profile" class="modal-backdrop" @click.self="profile = null">
      <div class="modal" role="dialog" aria-modal="true">
        <div class="card-head">
          <h2>{{ profile.account.deviceName }}</h2>
          <span class="mono small muted">{{ profile.filename }}</span>
          <button class="btn sm icon ghost spacer" :aria-label="t('action.cancel')" @click="profile = null">
            <Icon name="close" :size="14" />
          </button>
        </div>

        <div class="card-body">
          <div v-if="qr" class="qr">
            <img :src="qr" :alt="t('device.showQR')" :width="qrSize" :height="qrSize" />
            <p class="muted small">{{ t('device.qrHint') }}</p>
          </div>

          <div v-if="profile.username" class="creds">
            <div class="field">
              <label>{{ t('auth.username') }}</label>
              <div class="cred">
                <code>{{ profile.username }}</code>
                <button class="btn sm icon" :aria-label="t('action.copy')" @click="copy(profile.username)">
                  <Icon name="copy" :size="13" />
                </button>
              </div>
            </div>
            <div class="field">
              <label>{{ t('auth.password') }}</label>
              <div class="cred">
                <code>{{ profile.secret }}</code>
                <button class="btn sm icon" :aria-label="t('action.copy')" @click="copy(profile.secret)">
                  <Icon name="copy" :size="13" />
                </button>
              </div>
            </div>
          </div>

            <!-- Not on screen unless somebody deliberately asks for it.
                 The body is a private key with some routing around it: it is
                 handed over by QR or by the download button, and putting it in
                 front of whoever is looking at the panel -- over a shoulder, in
                 a screen share, in a screenshot of something else -- gives away
                 the tunnel to anyone who can read it. -->
            <div class="secret">
              <button class="btn sm ghost" @click="showConfig = !showConfig">
                <Icon :name="showConfig ? 'eyeOff' : 'eye'" :size="14" />
                {{ showConfig ? t('device.hideConfig') : t('device.revealConfig') }}
              </button>
              <p v-if="!showConfig" class="muted small">{{ t('device.configHidden') }}</p>
              <pre v-else class="config">{{ profile.body }}</pre>
            </div>
        </div>

        <div class="modal-foot">
          <button class="btn ghost" @click="copy(profile.body)">
            <Icon name="copy" :size="13" />{{ t('action.copy') }}
          </button>
          <button class="btn primary" :disabled="savingProfile" @click="saveProfile">
            <span v-if="savingProfile" class="spin sm"></span>
            <Icon v-else name="download" :size="13" />{{ t('device.downloadConfig') }}
          </button>
        </div>
      </div>
    </div>
  </template>

  <ConfirmDialog
    :open="!!ask"
    :title="ask?.title || ''"
    :body="ask?.body || ''"
    :subject="ask?.subject || ''"
    :consequences="ask?.consequences || []"
    :confirm-label="ask?.confirmLabel || ''"
    :busy="busy"
    @confirm="runConfirmed"
    @cancel="ask = null"
  />
</template>

<style scoped>
.back {
  display: inline-block;
  margin-bottom: 4px;
}
.back:hover {
  color: var(--accent-hover);
}
.quota-bar {
  padding: 0 16px 14px;
}
.card-body {
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.secret {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 8px;
}
.qr {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
}
.qr img {
  border-radius: var(--radius-sm);
  /* Deliberately white in every theme. A scanner reads the contrast between
     light and dark modules, and tinting the light ones is what makes a code
     fail on half the phones that try it. */
  background: #ffffff;
  padding: 8px;
  /* The white border sits around the image rather than being taken out of it:
     the code is sized to whole pixels per module and must not be squeezed. */
  box-sizing: content-box;
  max-width: 100%;
  height: auto;
}
.creds {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(190px, 1fr));
  gap: 12px;
}
.cred {
  display: flex;
  align-items: center;
  gap: 6px;
}
.cred code {
  flex: 1;
  min-width: 0;
  background: var(--surface-2);
  border: 1px solid var(--line);
  border-radius: var(--radius-sm);
  padding: 6px 9px;
  overflow-x: auto;
  white-space: nowrap;
}
.page-head p.row {
  gap: 6px;
}
</style>
