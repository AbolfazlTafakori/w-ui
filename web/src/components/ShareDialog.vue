<script setup>
import { ref, computed, onMounted } from 'vue'
import QRCode from 'qrcode'
import { api } from '../lib/api.js'
import { store, t, notify } from '../lib/store.js'
import { isOnline } from '../lib/format.js'
import Icon from './Icon.vue'

const props = defineProps({
  client: { type: Object, required: true },
})
const emit = defineEmits(['close'])

const devices = ref([])
const activeId = ref(null)
const profile = ref(null)
const qr = ref('')
const loading = ref(true)

const active = computed(() => devices.value.find((d) => d.id === activeId.value))

onMounted(async () => {
  try {
    // The list arrives with the client, but a share dialog opened from the
    // table may be looking at a stale copy, so the devices are re-read.
    const fresh = await api.client(props.client.id)
    devices.value = fresh.accounts || []
    if (devices.value.length) await select(devices.value[0].id)
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    loading.value = false
  }
})

async function select(id) {
  activeId.value = id
  qr.value = ''
  profile.value = null
  try {
    const p = await api.profile(id)
    profile.value = p
    // Only WireGuard clients can import a tunnel from a camera; OpenVPN
    // profiles carry no credentials, so a QR of one would be a dead end.
    if (props.client.protocol === 'wireguard') {
      qr.value = await QRCode.toDataURL(p.body, {
        margin: 1,
        width: 300,
        color: { dark: '#000000', light: '#ffffff' },
      })
    }
  } catch (err) {
    notify(err.message, 'error')
  }
}

async function copy(text, label) {
  try {
    await navigator.clipboard.writeText(text)
    notify(label || t('action.copied'), 'success')
  } catch {
    notify(t('action.copyFailed'), 'error')
  }
}
</script>

<template>
  <div class="modal-backdrop" @click.self="emit('close')">
    <div class="modal" role="dialog" aria-modal="true" aria-labelledby="share-title">
      <div class="card-head">
        <Icon name="share" :size="17" />
        <h2 id="share-title">{{ client.name }}</h2>
        <span class="tag proto">{{ client.protocol }}</span>
        <button class="btn sm icon ghost spacer" :aria-label="t('action.cancel')" @click="emit('close')">
          <Icon name="close" :size="15" />
        </button>
      </div>

      <div class="card-body">
        <div v-if="loading" class="empty"><span class="spin"></span></div>

        <div v-else-if="!devices.length" class="empty">{{ t('device.none') }}</div>

        <template v-else>
          <!-- One tab per device, because each carries its own key and address
               and they are not interchangeable. -->
          <div v-if="devices.length > 1" class="tabs" role="tablist">
            <button
              v-for="d in devices"
              :key="d.id"
              class="tab"
              :class="{ on: d.id === activeId }"
              role="tab"
              :aria-selected="d.id === activeId"
              @click="select(d.id)"
            >
              <i class="dot" :class="{ live: isOnline(d.lastHandshake) }"></i>
              {{ d.deviceName }}
            </button>
          </div>

          <div v-if="profile" class="pane">
            <div v-if="qr" class="qr">
              <img :src="qr" :alt="t('device.showQR')" width="230" height="230" />
              <p class="muted small">{{ t('device.qrHint') }}</p>
            </div>

            <div v-if="profile.username" class="creds">
              <div class="field">
                <label>{{ t('auth.username') }}</label>
                <div class="cred">
                  <code>{{ profile.username }}</code>
                  <button class="btn sm icon" :aria-label="t('action.copy')" @click="copy(profile.username)">
                    <Icon name="copy" :size="14" />
                  </button>
                </div>
              </div>
              <div class="field">
                <label>{{ t('auth.password') }}</label>
                <div class="cred">
                  <code>{{ profile.secret }}</code>
                  <button class="btn sm icon" :aria-label="t('action.copy')" @click="copy(profile.secret)">
                    <Icon name="copy" :size="14" />
                  </button>
                </div>
              </div>
            </div>

            <div class="meta row">
              <span class="mono small muted">{{ profile.filename }}</span>
              <span class="spacer mono small muted">{{ active?.ip }}</span>
            </div>

            <pre class="config">{{ profile.body }}</pre>
          </div>
        </template>
      </div>

      <div v-if="profile" class="modal-foot">
        <button class="btn ghost" @click="copy(profile.body)">
          <Icon name="copy" :size="14" />{{ t('action.copy') }}
        </button>
        <a class="btn primary" :href="api.profileDownloadUrl(activeId)" download>
          <Icon name="download" :size="14" />{{ t('device.downloadConfig') }}
        </a>
      </div>
    </div>
  </div>
</template>

<style scoped>
.card-body {
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.card-head svg {
  color: var(--muted);
}
.tabs {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
  border-bottom: 1px solid var(--line-soft);
  padding-bottom: 12px;
}
.tab {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  padding: 7px 13px;
  border: 1px solid var(--line);
  border-radius: var(--radius-sm);
  background: var(--surface-2);
  color: var(--ink-2);
  font: inherit;
  font-size: var(--t-sm);
  cursor: pointer;
}
.tab:hover {
  background: var(--surface-3);
}
.tab.on {
  background: var(--accent-soft);
  border-color: var(--accent-line);
  color: var(--accent-hover);
  font-weight: 600;
}
.dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--faint);
}
.dot.live {
  background: var(--ok);
}
.pane {
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.qr {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
}
.qr img {
  border-radius: var(--radius-sm);
  background: #fff;
  padding: 9px;
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
  padding: 7px 10px;
  overflow-x: auto;
  white-space: nowrap;
}
.meta {
  gap: 10px;
}
</style>
