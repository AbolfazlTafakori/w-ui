<script setup>
// Backup and restore, from the overview: a fresh archive straight to the
// browser, or one brought in, confirmed, and followed through the restart.
import { ref } from 'vue'
import { RouterLink } from 'vue-router'
import { api, saveFile, uploadBackup } from '../../lib/api.js'
import { waitForRestart } from '../../lib/restart.js'
import { store, t } from '../../lib/store.js'
import { bytes } from '../../lib/format.js'
import Icon from '../Icon.vue'

const emit = defineEmits(['close'])

// Backup and restore, laid out as the classic panel's dialog: two lines, each
// one button. "Back up" takes a fresh archive and hands it to the browser in
// the same click -- it used to take one on the server and offer the newest
// archive as a second step, which could be an older, automatic one. "Restore"
// picks a file, says what it is and what will happen, and only then replaces
// anything; it follows the panel through its restart and reloads once it is
// back, rather than after a guess of seven seconds.
//
// backup.step is 'choose' (the two lines), 'confirm' (a file picked),
// 'working' (backing up, uploading or restoring) or 'failed'.
const backup = ref({ step: 'choose', line: '', share: null, file: null, error: '', done: '' })
const keepAddresses = ref(true)
const restoreInput = ref(null)


function closeBackup() {
  if (backup.value?.step === 'working') return
  emit('close')
}

// Fetched rather than linked: the archive holds every key on the server, and a
// plain link carries no session.
async function backupNow() {
  if (!backup.value) return
  backup.value = { ...backup.value, step: 'working', line: t('overview.backingUp'), share: null, done: '', error: '' }
  try {
    const a = await api.post('/api/backups')
    backup.value.line = t('overview.preparingDownload')
    await saveFile(`/api/backups/${encodeURIComponent(a.name)}`, a.name)
    if (backup.value) backup.value = { ...backup.value, step: 'choose', done: t('overview.backupDownloaded').replace('{name}', a.name) }
  } catch (e) {
    if (backup.value) backup.value = { ...backup.value, step: 'choose', error: e.message }
  }
}

function pickRestore() {
  restoreInput.value?.click()
}

function restoreChosen(event) {
  const file = event.target.files?.[0]
  event.target.value = ''
  if (!file || !backup.value) return
  backup.value = { ...backup.value, step: 'confirm', file, error: '', done: '' }
}

// The upload's own message when the panel gave one; otherwise what kind of
// failure it was.
async function upload(file, onShare) {
  try {
    return await uploadBackup(file, onShare)
  } catch (e) {
    if (e.kind === 'network') throw new Error(t('overview.uploadCut'))
    throw new Error(e.message || t('overview.importFailed'))
  }
}

async function restoreNow() {
  const file = backup.value?.file
  if (!file) return
  backup.value = { ...backup.value, step: 'working', line: t('overview.uploading'), share: 0, error: '' }
  try {
    const stored = await upload(file, (share) => {
      if (backup.value) backup.value.share = share
    })
    backup.value = { ...backup.value, line: t('overview.restoring'), share: null }
    const params = keepAddresses.value ? '' : '?keepAddresses=false'
    await api.post(`/api/backups/${encodeURIComponent(stored.name)}/restore${params}`)
  } catch (e) {
    if (backup.value) backup.value = { ...backup.value, step: 'failed', error: e.message }
    return
  }
  // Restored. The panel ends itself to start again on the restored files.
  backup.value = { ...backup.value, line: t('overview.restarting') }
  if (!(await waitForRestart()) && backup.value) {
    backup.value = { ...backup.value, step: 'failed', error: t('overview.restoreNotBack') }
  }
}
</script>

<template>
  <!-- Backup and restore, as the classic panel's dialog: one line to back up,
       one to restore, each a single button. -->
  <div class="modal-backdrop" @click.self="closeBackup">
    <div class="modal narrow" role="dialog" aria-modal="true" aria-labelledby="bk-title">
      <div class="card-head">
        <h2 id="bk-title">{{ t('overview.backupRestore') }}</h2>
        <button class="btn sm icon ghost spacer" :aria-label="t('common.close')"
                :disabled="backup.step === 'working'" @click="closeBackup">
          <Icon name="close" :size="15" />
        </button>
      </div>

      <div class="card-body bk-body">
        <template v-if="backup.step === 'choose'">
          <p v-if="backup.done" class="bk-done" role="status">{{ backup.done }}</p>
          <p v-if="backup.error" class="bk-error" role="alert">{{ backup.error }}</p>

          <div class="bk-item">
            <div class="bk-meta">
              <div class="bk-title">{{ t('overview.backupLine') }}</div>
              <p class="bk-desc">{{ t('overview.backupLineDesc') }}</p>
            </div>
            <button type="button" class="btn primary" @click="backupNow">
              <Icon name="download" :size="15" /><span>{{ t('overview.backupButton') }}</span>
            </button>
          </div>

          <div class="bk-item">
            <div class="bk-meta">
              <div class="bk-title">{{ t('overview.restoreLine') }}</div>
              <p class="bk-desc">{{ t('overview.restoreLineDesc') }}</p>
            </div>
            <button type="button" class="btn" @click="pickRestore">
              <Icon name="upload" :size="15" /><span>{{ t('overview.restoreButton') }}</span>
            </button>
            <input ref="restoreInput" type="file" class="sr-only" tabindex="-1" aria-hidden="true"
                   accept=".gz,.tar.gz,application/gzip" @change="restoreChosen" />
          </div>

          <RouterLink to="/settings/backups" class="bk-all" @click="emit('close')">
            {{ t('overview.allBackups') }}
          </RouterLink>
        </template>

        <!-- A file picked: what it is, what happens, and the one choice that
             matters when a panel moves to another machine. Nothing is sent
             until Restore is pressed. -->
        <template v-else-if="backup.step === 'confirm'">
          <div class="bk-file">
            <Icon name="database" :size="16" />
            <span class="ltr bk-file-name">{{ backup.file.name }}</span>
            <span class="muted">{{ bytes(backup.file.size, store.locale) }}</span>
          </div>
          <p class="bk-warn">{{ t('overview.restoreWarn') }}</p>
          <label class="bk-keep">
            <input v-model="keepAddresses" type="checkbox" />
            <span>
              <b>{{ t('overview.keepAddresses') }}</b>
              <span class="bk-desc">{{ t('overview.keepAddressesDesc') }}</span>
            </span>
          </label>
        </template>

        <template v-else-if="backup.step === 'working'">
          <div class="update-progress" role="status" aria-live="polite">
            <div class="update-bar" :class="{ busy: backup.share === null }" role="progressbar"
                 aria-valuemin="0" aria-valuemax="100" :aria-valuenow="backup.share">
              <i :style="{ width: (backup.share === null ? 100 : backup.share) + '%' }"></i>
            </div>
            <p class="muted small">
              {{ backup.line }}<template v-if="backup.share !== null"> {{ backup.share }}%</template>
            </p>
          </div>
        </template>

        <template v-else-if="backup.step === 'failed'">
          <p class="bk-error" role="alert">{{ backup.error }}</p>
        </template>
      </div>

      <div v-if="backup.step === 'confirm'" class="modal-foot">
        <button type="button" class="btn ghost" @click="backup.step = 'choose'">{{ t('action.cancel') }}</button>
        <button type="button" class="btn danger" @click="restoreNow">{{ t('overview.restoreNow') }}</button>
      </div>
      <div v-else-if="backup.step === 'failed'" class="modal-foot">
        <button type="button" class="btn ghost" @click="closeBackup">{{ t('common.close') }}</button>
        <button type="button" class="btn" @click="backup.step = 'choose'">{{ t('update.tryAgain') }}</button>
      </div>
    </div>
  </div>
</template>
