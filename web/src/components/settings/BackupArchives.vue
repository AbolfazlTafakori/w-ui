<script setup>
// The backups kept on this server, from the settings page: the backup
// password, the archives with their download, restore and delete, and
// bringing one in from elsewhere. How often one is taken and how many are
// kept are settings, saved with the rest of the page.
import { onMounted, ref } from 'vue'
import { api, saveFile, uploadBackup as sendBackup } from '../../lib/api.js'
import { waitForRestart } from '../../lib/restart.js'
import { t, notify } from '../../lib/store.js'
import Icon from '../Icon.vue'
import ConfirmDialog from '../ConfirmDialog.vue'

onMounted(loadBackups)

const backups = ref([])
const backupBusy = ref(false)
const restoring = ref(null)
const uploading = ref(false)
const ask = ref(null)

async function loadBackups() {
  try {
    backups.value = await api.get('/api/backups')
  } catch {
    backups.value = []
  }
  await loadBackupPassword()
}

// The backup password: archives are encrypted with it from the moment it is
// set. The server never sends it back, only whether there is one.
const backupPassword = ref({ set: false, minLength: 10 })
const newBackupPassword = ref('')
const passwordBusy = ref(false)
async function loadBackupPassword() {
  try {
    backupPassword.value = await api.get('/api/backups/password', { background: true })
  } catch {
    backupPassword.value = { set: false, minLength: 10 }
  }
}
async function saveBackupPassword(clear) {
  passwordBusy.value = true
  try {
    backupPassword.value = await api.put('/api/backups/password', { password: clear ? '' : newBackupPassword.value })
    newBackupPassword.value = ''
    notify(t(clear ? 'settings.backupPasswordRemoved' : 'settings.backupPasswordSaved'), clear ? 'warning' : 'success')
  } catch (err) {
    notify(err.message, 'error')
  } finally {
    passwordBusy.value = false
  }
}
async function makeBackup() {
  backupBusy.value = true
  try {
    await api.post('/api/backups')
    await loadBackups()
    notify(t('settings.backupTaken'), 'success')
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    backupBusy.value = false
  }
}
async function removeBackup(name) {
  try {
    await api.del(`/api/backups/${encodeURIComponent(name)}`)
    await loadBackups()
  } catch (e) {
    notify(e.message, 'error')
  }
}
// The archive holds every key and credential, so it is fetched with the
// session token rather than linked.
async function downloadBackup(name) {
  try {
    await saveFile(`/api/backups/${encodeURIComponent(name)}`, name)
  } catch (e) {
    notify(e.message, 'error')
  }
}
async function restoreBackup(name) {
  restoring.value = name
  try {
    const res = await api.post(`/api/backups/${encodeURIComponent(name)}/restore`)
    notify(t('settings.restoreStarted', { n: res?.safetyCopy || '' }), 'success')
  } catch (e) {
    restoring.value = null
    notify(e.message, 'error')
    return
  }
  // The panel ends itself to start again on the restored files; the page
  // follows it rather than reloading after a guess.
  if (!(await waitForRestart())) {
    restoring.value = null
    notify(t('overview.restoreNotBack'), 'error')
  }
}
async function uploadBackup(event) {
  const file = event.target.files?.[0]
  event.target.value = ''
  if (!file) return
  uploading.value = true
  try {
    try {
      await sendBackup(file)
    } catch (e) {
      throw new Error(e.message || t('settings.uploadFailed'))
    }
    await loadBackups()
    notify(t('settings.uploaded'), 'success')
  } catch (e) {
    notify(e.message, 'error')
  } finally {
    uploading.value = false
  }
}

function humanBytes(n) {
  if (!n) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  let i = 0
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024
    i++
  }
  return `${n.toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}
</script>

<template>
  <div class="backup-archives">
    <div class="setting-list-item"><div class="arow">
      <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('settings.backupPassword') }}</div><div class="setting-list-description">{{ t('settings.backupPasswordDesc') }}</div></div></div>
      <div class="acol"><div class="aspace-v">
        <div><span class="atag" :class="backupPassword.set ? 'green' : 'red'">{{ backupPassword.set ? t('settings.backupPasswordSet') : t('settings.backupPasswordNone') }}</span></div>
        <div class="acompact">
          <label class="ainput"><input id="backup-password" v-model="newBackupPassword" type="password" autocomplete="new-password" class="ltr" :placeholder="t('settings.backupPasswordPlaceholder', { n: backupPassword.minLength })" /></label>
          <button class="abtn primary" :disabled="passwordBusy || newBackupPassword.length < backupPassword.minLength" @click="saveBackupPassword(false)">{{ t('settings.backupPasswordSave') }}</button>
        </div>
        <div v-if="backupPassword.set"><button class="linkbtn danger" :disabled="passwordBusy" @click="saveBackupPassword(true)">{{ t('settings.backupPasswordRemove') }}</button></div>
      </div></div>
    </div></div>
    <div class="setting-list-item"><div class="arow">
      <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('settings.backupsOnDisk') }}</div><div class="setting-list-description">{{ t('settings.backupsWarning') }}</div></div></div>
      <div class="acol"><div class="aspace-v">
        <div><button class="abtn primary" :disabled="backupBusy" @click="makeBackup">{{ backupBusy ? t('settings.backingUp') : t('settings.backupNow') }}</button></div>
        <ul v-if="backups.length" class="backup-list">
          <li v-for="b in backups" :key="b.name">
            <span class="backup-name ltr">{{ b.name }}</span>
            <span v-if="b.encrypted" class="atag green">{{ t('settings.encrypted') }}</span>
            <span class="backup-size ltr">{{ humanBytes(b.size) }}</span>
            <button class="linkbtn" @click="downloadBackup(b.name)">{{ t('settings.download') }}</button>
            <button class="linkbtn" :disabled="!!restoring" @click="ask = b.name">{{ t('settings.restore') }}</button>
            <button class="linkbtn danger" :disabled="!!restoring" @click="removeBackup(b.name)">{{ t('common.delete') }}</button>
          </li>
        </ul>
        <p v-else class="muted">{{ t('settings.noBackups') }}</p>
        <label class="abtn upload-btn">
          <Icon name="upload" :size="15" />
          <span>{{ uploading ? t('settings.uploading') : t('settings.uploadBackup') }}</span>
          <input type="file" accept=".gz,.tar.gz,.enc,application/gzip" :disabled="uploading || !!restoring" @change="uploadBackup" />
        </label>
      </div></div>
    </div></div>

    <ConfirmDialog
      :open="!!ask"
      :title="t('settings.restoreTitle')"
      :body="t('settings.restoreBody')"
      :confirm-label="t('settings.restoreConfirm')"
      :danger="true"
      :busy="!!restoring"
      @confirm="() => { const n = ask; ask = null; restoreBackup(n) }"
      @cancel="ask = null"
    />
  </div>
</template>
