<script setup>
// The settings page's Email tab: the mail server, and which events it
// is told of.
import { ref } from 'vue'
import { useSettings, PLACEHOLDER, eventGroups } from './context.js'
import { api } from '../../lib/api.js'
import { t } from '../../lib/store.js'
import AntIcon from '../AntIcon.vue'
import Toggle from '../Toggle.vue'

const { form, saved, inner, isDefault, saveIfDirty, showSecret, secretConfigured, secretClearArmed, toggleClear, hasKind, toggleKind, groupCount, toggleGroup } = useSettings()

const mailTesting = ref(false)
const mailResult = ref(null)
async function testMail() {
  mailTesting.value = true
  mailResult.value = null
  try {
    await saveIfDirty()
    const res = await api.post('/api/settings/mail/test')
    mailResult.value = { success: !!res.ok, msg: res.ok ? t('settings.testOk') : res.error }
  } catch (e) {
    mailResult.value = { success: false, msg: e.message }
  } finally {
    mailTesting.value = false
  }
}
</script>

<template>
    <div class="atabs-nav"><div class="atabs-list">
      <button class="atab" :class="{ active: inner.email === '1' }" @click="inner.email = '1'"><AntIcon name="SettingOutlined" /><span>{{ t('set.smtpSettings') }}</span></button>
      <button class="atab" :class="{ active: inner.email === '2' }" @click="inner.email = '2'"><AntIcon name="MailOutlined" /><span>{{ t('set.emailNotifications') }}</span></button>
    </div></div>

    <template v-if="inner.email === '1'">
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.smtpEnable') }}</div><div class="setting-list-description">{{ t('set.smtpEnableDesc') }}</div></div></div>
        <div class="acol"><Toggle v-model="form.mailEnabled" :label="t('set.smtpEnable')" /></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.smtpHost') }}</div><div class="setting-list-description">{{ t('set.smtpHostDesc') }}</div></div></div>
        <div class="acol"><label class="ainput block"><input v-model="form.mailHost" class="ltr" placeholder="smtp.gmail.com" /></label></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.smtpPort') }}<span v-if="isDefault('mailPort')" class="atag">{{ t('set.defaultTag') }}</span></div><div class="setting-list-description">{{ t('set.smtpPortDesc') }}</div></div></div>
        <div class="acol"><label class="ainput number"><input v-model.number="form.mailPort" type="number" min="1" max="65535" class="ltr" /></label></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.smtpUsername') }}</div><div class="setting-list-description">{{ t('set.smtpUsernameDesc') }}</div></div></div>
        <div class="acol"><label class="ainput block"><input v-model="form.mailUsername" class="ltr" placeholder="user@gmail.com" autocomplete="off" /></label></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.smtpPassword') }}</div><div class="setting-list-description">{{ secretConfigured('mailPassword') ? t('set.smtpPasswordConfigured') : t('set.smtpPasswordDesc') }}</div></div></div>
        <div class="acol"><div class="acompact">
          <label class="ainput"><input :value="form.mailPassword === PLACEHOLDER ? '' : form.mailPassword" :type="showSecret.mail ? 'text' : 'password'" autocomplete="off" class="ltr" :placeholder="secretConfigured('mailPassword') ? t('set.smtpPasswordPlaceholder') : ''" @input="form.mailPassword = $event.target.value" /><button type="button" class="ainput-clear" @click="showSecret.mail = !showSecret.mail"><AntIcon :name="showSecret.mail ? 'EyeOutlined' : 'EyeInvisibleOutlined'" /></button></label>
          <button v-if="saved.mailPassword === PLACEHOLDER" type="button" class="abtn" :class="{ danger: secretClearArmed('mailPassword') }" @click="toggleClear('mailPassword')">{{ secretClearArmed('mailPassword') ? t('set.secretClearUndo') : t('set.secretClear') }}</button>
        </div></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.smtpFrom') }}</div><div class="setting-list-description">{{ t('set.smtpFromDesc') }}</div></div></div>
        <div class="acol"><label class="ainput block"><input v-model="form.mailFrom" class="ltr" placeholder="user@gmail.com" /></label></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.smtpFromName') }}</div><div class="setting-list-description">{{ t('set.smtpFromNameDesc') }}</div></div></div>
        <div class="acol"><label class="ainput block"><input v-model="form.mailFromName" placeholder="W-UI" /></label></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.smtpTo') }}</div><div class="setting-list-description">{{ t('set.smtpToDesc') }}</div></div></div>
        <div class="acol"><label class="ainput block"><input v-model="form.mailTo" class="ltr" placeholder="admin@example.com, ops@example.com" /></label></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.smtpEncryption') }}</div><div class="setting-list-description">{{ t('set.smtpEncryptionDesc') }}</div></div></div>
        <div class="acol"><div class="aselect"><select v-model="form.mailEncryption" :aria-label="t('set.smtpEncryption')">
          <option value="none">{{ t('set.smtpEncryptionNone') }}</option>
          <option value="starttls">{{ t('set.smtpEncryptionStartTLS') }}</option>
          <option value="tls">{{ t('set.smtpEncryptionTLS') }}</option>
        </select></div></div>
      </div></div>
      <div class="aspace-v" style="margin-top: 16px">
        <div><button class="abtn primary" :disabled="mailTesting" @click="testMail"><span v-if="mailTesting" class="spin sm"></span><AntIcon v-else name="SendOutlined" /><span>{{ t('set.testSmtp') }}</span></button></div>
        <div v-if="mailResult" class="aalert" :class="mailResult.success ? 'success' : 'error'">
          <AntIcon :name="mailResult.success ? 'CheckCircleFilled' : 'CloseCircleFilled'" />
          <div class="aalert-body"><span class="aalert-title">{{ mailResult.msg }}</span></div>
          <button type="button" class="aalert-close" @click="mailResult = null"><AntIcon name="CloseOutlined" /></button>
        </div>
      </div>
    </template>

    <template v-else>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.smtpEventBusNotify') }}</div><div class="setting-list-description">{{ t('set.smtpEventBusNotifyDesc') }}</div></div></div>
        <div class="acol">
          <div class="notif-grid">
            <div v-for="g in eventGroups" :key="g.title" class="acard small notif-card">
              <div class="acard-head small">
                <span class="notif-title"><AntIcon :name="g.icon" /> {{ t(g.title) }}</span>
                <span class="notif-extra"><span class="atag">{{ groupCount('mailKinds', g) }}/{{ g.events.length }}</span><input type="checkbox" class="acheck" :checked="groupCount('mailKinds', g) === g.events.length" :indeterminate.prop="groupCount('mailKinds', g) > 0 && groupCount('mailKinds', g) < g.events.length" @change="toggleGroup('mailKinds', g)" /></span>
              </div>
              <div class="acard-body"><div class="aspace-v">
                <div v-for="e in g.events" :key="e.key">
                  <label class="acheckbox"><input type="checkbox" :checked="hasKind('mailKinds', e.key)" @change="toggleKind('mailKinds', e.key)" />{{ t(e.label) }}</label>
                  <div v-if="e.setting && hasKind('mailKinds', e.key)" style="padding-left: 24px; margin-top: 4px">
                    <label class="ainput number sm" style="width: 80px"><input v-model.number="form[e.setting]" type="number" :min="e.min" :max="e.max" class="ltr" /></label>
                  </div>
                </div>
              </div></div>
            </div>
          </div>
        </div>
      </div></div>
    </template>
</template>
