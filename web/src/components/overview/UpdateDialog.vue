<script setup>
// What release is available, and what installing it involves; the install
// followed to the panel coming back on the new build. The state is the
// page's (lib/update.js): closing this does not stop an install.
import { useUpdateState } from '../../lib/update.js'
import { t } from '../../lib/store.js'
import Icon from '../Icon.vue'

defineProps({
  // The version the page reads, for when the release list did not answer.
  current: { type: String, default: '' },
})
const { update, updateBusy, updateOpen, installing, applyUpdate, retryInstall, installShare, installLine, updateCommand, copyUpdateCommand } = useUpdateState()
</script>

<template>
  <!-- What release is available, and what installing it involves. -->
  <div class="modal-backdrop" @click.self="updateOpen = false">
    <div class="modal narrow" role="dialog" aria-modal="true" aria-labelledby="up-title">
      <div class="card-head">
        <h2 id="up-title">{{ t('update.title') }}</h2>
        <button class="btn sm icon ghost spacer" :aria-label="t('common.close')" @click="updateOpen = false">
          <Icon name="close" :size="15" />
        </button>
      </div>

      <div class="card-body">
        <p class="muted small ltr">
          {{ t('update.running') }}: <b>{{ update?.current || current }}</b>
          <template v-if="update?.latest"> · {{ t('update.newest') }}: <b>{{ update.latest }}</b></template>
        </p>

        <p v-if="update?.notice" class="log-notice">{{ update.notice }}</p>

        <!-- Said before the button rather than after pressing it. -->
        <p v-if="update && !update.signed" class="log-notice">{{ t('update.unsigned') }}</p>

        <!-- A server where the panel may not replace its own binary and the
             helper that does it is not installed yet: said with the one
             command that puts it right, rather than a button that fails. -->
        <template v-else-if="update?.cannotInstall && update?.available && !installing">
          <p class="log-notice">{{ t('update.needsHelper') }}</p>
          <div class="update-cmd">
            <code class="ltr">{{ updateCommand }}</code>
            <button type="button" class="btn sm" @click="copyUpdateCommand">{{ t('action.copy') }}</button>
          </div>
        </template>

        <p v-else-if="update && !update.available" class="muted">{{ t('update.upToDate') }}</p>

        <template v-else-if="update?.available && !installing">
          <p class="hint">{{ t('update.whatHappens') }}</p>
          <pre v-if="update.notes" class="update-notes ltr">{{ update.notes }}</pre>
        </template>

        <!-- The install, followed to the panel coming back on the new build.
             Closing the dialog does not stop it; the page goes on following
             and reloads when it is done. -->
        <div v-if="installing" class="update-progress" role="status" aria-live="polite">
          <template v-if="installing.stage === 'failed'">
            <p class="log-notice">{{ t('update.failed') }}</p>
            <p class="update-error ltr">{{ installing.error }}</p>
          </template>
          <template v-else>
            <div class="update-bar" :class="{ busy: installShare === null || installing.stage !== 'downloading' }"
                 role="progressbar" aria-valuemin="0" aria-valuemax="100"
                 :aria-valuenow="installing.stage === 'downloading' ? installShare : null">
              <i :style="{ width: (installing.stage === 'downloading' && installShare !== null ? installShare : 100) + '%' }"></i>
            </div>
            <p class="muted small">{{ installLine }}</p>
          </template>
        </div>
      </div>

      <div class="modal-foot">
        <template v-if="installing && installing.stage === 'failed'">
          <button type="button" class="btn ghost" @click="updateOpen = false">{{ t('common.close') }}</button>
          <button type="button" class="btn primary" @click="retryInstall">{{ t('update.tryAgain') }}</button>
        </template>
        <template v-else-if="installing">
          <button type="button" class="btn ghost" @click="updateOpen = false">{{ t('update.hide') }}</button>
        </template>
        <template v-else>
          <button type="button" class="btn ghost" @click="updateOpen = false">{{ t('action.cancel') }}</button>
          <button
            class="btn primary"
            :disabled="updateBusy || !update?.available || !update?.signed || !!update?.cannotInstall"
            @click="applyUpdate"
          >
            <span v-if="updateBusy" class="spin"></span>
            <span v-else>{{ t('update.install') }}</span>
          </button>
        </template>
      </div>
    </div>
  </div>
</template>
