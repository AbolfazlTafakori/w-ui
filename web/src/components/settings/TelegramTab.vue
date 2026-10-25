<script setup>
// The settings page's bot tab: the bot, when its report and backups are
// sent, and which events it is told of. The same tab serves the Telegram bot
// and the Bale one (the Iranian messenger whose bot API is Telegram's): each
// has its own fields, named here, and its own words for what differs.
import { computed, ref } from 'vue'
import { useSettings, PLACEHOLDER, eventGroups } from './context.js'
import { api } from '../../lib/api.js'
import { store, t } from '../../lib/store.js'
import AntIcon from '../AntIcon.vue'
import Toggle from '../Toggle.vue'

const props = defineProps({ platform: { type: String, default: 'telegram' } })
const bale = computed(() => props.platform === 'bale')

const { form, saved, inner, saveIfDirty, showSecret, secretConfigured, secretClearArmed, toggleClear, hasKind, toggleKind, groupCount, toggleGroup } = useSettings()

// The settings fields this tab edits, and the labels that name its platform.
const f = computed(() => bale.value
  ? { enabled: 'baleEnabled', token: 'baleBotToken', chat: 'baleChatId', lang: 'baleLang', runTime: 'baleRunTime', backup: 'baleBackup', backupTime: 'baleBackupTime', kinds: 'baleKinds' }
  : { enabled: 'notifyEnabled', token: 'notifyBotToken', chat: 'notifyChatId', lang: 'notifyLang', runTime: 'notifyRunTime', backup: 'notifyBackup', backupTime: 'notifyBackupTime', kinds: 'notifyKinds' })
const L = computed(() => bale.value
  ? { enable: 'set.baleBotEnable', enableDesc: 'set.baleBotEnableDesc', token: 'set.baleToken', tokenDesc: 'set.baleTokenDesc', chatDesc: 'set.baleChatIdDesc', lang: 'set.baleBotLanguage', timeDesc: 'set.baleNotifyTimeDesc', events: 'set.baleEventBusNotify', eventsDesc: 'set.baleEventBusNotifyDesc' }
  : { enable: 'set.telegramBotEnable', enableDesc: 'set.telegramBotEnableDesc', token: 'set.telegramToken', tokenDesc: 'set.telegramTokenDesc', chatDesc: 'set.telegramChatIdDesc', lang: 'set.telegramBotLanguage', timeDesc: 'set.telegramNotifyTimeDesc', events: 'set.tgEventBusNotify', eventsDesc: 'set.tgEventBusNotifyDesc' })
// Each tab remembers its own inner tab.
const tab = computed({
  get: () => inner.value[props.platform] || '1',
  set: (v) => { inner.value[props.platform] = v },
})

// ── notification time: their preset select, interval or crontab ─────────────
const notifyMode = computed({
  get: () => {
    const v = form.value?.[f.value.runTime] || ''
    if (v.startsWith('@every ')) return 'every'
    if (['@hourly', '@daily', '@weekly', '@monthly'].includes(v)) return v
    return 'custom'
  },
  set: (m) => {
    if (m === 'every') form.value[f.value.runTime] = '@every 30m'
    else if (m === 'custom') form.value[f.value.runTime] = '0 30 8 * * *'
    else form.value[f.value.runTime] = m
  },
})
const everyNum = computed({
  get: () => Number((form.value?.[f.value.runTime] || '').replace('@every ', '').replace(/[a-z]+$/, '')) || 1,
  set: (n) => {
    form.value[f.value.runTime] = `@every ${Math.max(1, Math.round(Number(n) || 1))}${everyUnit.value}`
  },
})
const everyUnit = computed({
  get: () => (form.value?.[f.value.runTime] || '').replace('@every ', '').replace(/^\d+/, '') || 'm',
  set: (u) => {
    form.value[f.value.runTime] = `@every ${everyNum.value}${u}`
  },
})

// ── automatic backup time: every few hours, a time each day or week, crontab ──
// Kept as the same schedule string the report uses; these read and write it.
function readBackupTime(v) {
  v = (v || '').trim()
  let m = v.match(/^@every (\d+)h$/)
  if (m) return { mode: 'hours', hours: Number(m[1]), time: '03:00', day: 5 }
  if (v === '@daily' || v === '@midnight') return { mode: 'daily', hours: 24, time: '00:00', day: 5 }
  if (v === '@weekly') return { mode: 'weekly', hours: 24, time: '00:00', day: 0 }
  m = v.match(/^(?:0 )?(\d{1,2}) (\d{1,2}) \* \* (\*|[0-6])$/)
  if (m && Number(m[1]) < 60 && Number(m[2]) < 24) {
    const time = `${m[2].padStart(2, '0')}:${m[1].padStart(2, '0')}`
    if (m[3] === '*') return { mode: 'daily', hours: 24, time, day: 5 }
    return { mode: 'weekly', hours: 24, time, day: Number(m[3]) }
  }
  return { mode: 'custom', hours: 24, time: '03:00', day: 5 }
}
function writeBackupTime(p) {
  const [h, min] = (p.time || '00:00').split(':').map(Number)
  if (p.mode === 'hours') return `@every ${Math.min(720, Math.max(1, Math.round(Number(p.hours) || 1)))}h`
  if (p.mode === 'daily') return `0 ${min} ${h} * * *`
  if (p.mode === 'weekly') return `0 ${min} ${h} * * ${p.day}`
  return form.value[f.value.backupTime]
}
function setBackupTime(change) {
  const p = { ...readBackupTime(form.value[f.value.backupTime]), ...change }
  // Custom starts from the line already there, to be edited.
  if (change.mode === 'custom') {
    backupCustom.value = true
    return
  }
  backupCustom.value = false
  form.value[f.value.backupTime] = writeBackupTime(p)
}
// Custom stays chosen while its line is typed, even when that line happens to
// read as one of the others.
const backupCustom = ref(false)
const backupTime = computed(() => {
  const p = readBackupTime(form.value?.[f.value.backupTime])
  if (backupCustom.value) p.mode = 'custom'
  return p
})
const weekdays = computed(() => {
  const f = new Intl.DateTimeFormat(store.locale === 'fa' ? 'fa-IR' : 'en-GB', { weekday: 'long', timeZone: 'UTC' })
  // 4 January 1970 was a Sunday, day 0 to crontab.
  return [0, 1, 2, 3, 4, 5, 6].map((d) => ({ d, name: f.format(new Date(Date.UTC(1970, 0, 4 + d))) }))
})


const testLoading = ref(false)
const testResult = ref(null)
async function testTgBot() {
  testLoading.value = true
  testResult.value = null
  try {
    await saveIfDirty()
    const res = await api.post(bale.value ? '/api/settings/bale/test' : '/api/settings/notify/test')
    testResult.value = { success: !!res.ok, msg: res.ok ? t('settings.testOk') : res.error }
  } catch (e) {
    testResult.value = { success: false, msg: e.message }
  } finally {
    testLoading.value = false
  }
}
</script>

<template>
    <div class="atabs-nav"><div class="atabs-list">
      <button class="atab" :class="{ active: tab === '1' }" @click="tab = '1'"><AntIcon name="SettingOutlined" /><span>{{ t('set.panelSettings') }}</span></button>
      <button class="atab" :class="{ active: tab === '2' }" @click="tab = '2'"><AntIcon name="BellOutlined" /><span>{{ t('set.notifications') }}</span></button>
      <button class="atab" :class="{ active: tab === '3' }" @click="tab = '3'"><AntIcon name="CloudUploadOutlined" /><span>{{ t('set.tgBackupTab') }}</span></button>
    </div></div>

    <template v-if="tab === '1'">
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t(L.enable) }}</div><div class="setting-list-description">{{ t(L.enableDesc) }}</div></div></div>
        <div class="acol"><Toggle v-model="form[f.enabled]" :label="t(L.enable)" /></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t(L.token) }}</div><div class="setting-list-description">{{ secretConfigured(f.token) ? t('set.telegramTokenConfigured') : t(L.tokenDesc) }}</div></div></div>
        <div class="acol"><div class="acompact">
          <label class="ainput"><input :value="form[f.token] === PLACEHOLDER ? '' : form[f.token]" :type="showSecret[platform] ? 'text' : 'password'" autocomplete="off" spellcheck="false" class="ltr" :placeholder="secretConfigured(f.token) ? t('set.telegramTokenPlaceholder') : ''" @input="form[f.token] = $event.target.value" /><button type="button" class="ainput-clear" @click="showSecret[platform] = !showSecret[platform]"><AntIcon :name="showSecret[platform] ? 'EyeOutlined' : 'EyeInvisibleOutlined'" /></button></label>
          <button v-if="saved[f.token] === PLACEHOLDER" type="button" class="abtn" :class="{ danger: secretClearArmed(f.token) }" @click="toggleClear(f.token)">{{ secretClearArmed(f.token) ? t('set.secretClearUndo') : t('set.secretClear') }}</button>
        </div></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.telegramChatId') }}</div><div class="setting-list-description">{{ t(L.chatDesc) }}</div></div></div>
        <div class="acol"><label class="ainput block"><input v-model="form[f.chat]" class="ltr" /></label></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t(L.lang) }}</div></div></div>
        <div class="acol"><div class="aselect"><select v-model="form[f.lang]" :aria-label="t(L.lang)">
          <option value="en">🇬🇧&nbsp;&nbsp;English</option>
          <option value="fa">🇮🇷&nbsp;&nbsp;فارسی</option>
        </select></div></div>
      </div></div>
      <div v-if="!bale" class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.telegramAPIServer') }}</div><div class="setting-list-description">{{ t('set.telegramAPIServerDesc') }}</div></div></div>
        <div class="acol"><label class="ainput block"><input v-model="form.notifyAPIServer" class="ltr" placeholder="https://api.example.com" /></label></div>
      </div></div>
      <div class="aspace-v" style="margin-top: 16px">
        <div><button class="abtn primary" :disabled="testLoading" @click="testTgBot"><span v-if="testLoading" class="spin sm"></span><AntIcon v-else name="SendOutlined" /><span>{{ t('set.testTgBot') }}</span></button></div>
        <div v-if="testResult" class="aalert" :class="testResult.success ? 'success' : 'error'">
          <AntIcon :name="testResult.success ? 'CheckCircleFilled' : 'CloseCircleFilled'" />
          <div class="aalert-body"><span class="aalert-title">{{ testResult.msg }}</span></div>
          <button type="button" class="aalert-close" @click="testResult = null"><AntIcon name="CloseOutlined" /></button>
        </div>
      </div>
    </template>

    <template v-else-if="tab === '3'">
      <div v-if="!form[f.enabled]" class="aalert warning" style="margin-bottom: 16px">
        <AntIcon name="WarningOutlined" />
        <div class="aalert-body"><span class="aalert-title">{{ t('set.tgBackupBotOff') }}</span></div>
      </div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.tgAutoBackup') }}</div><div class="setting-list-description">{{ t('set.tgAutoBackupDesc') }}</div></div></div>
        <div class="acol"><Toggle v-model="form[f.backup]" :label="t('set.tgAutoBackup')" /></div>
      </div></div>
      <div v-if="form[f.backup]" class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.tgBackupTime') }}</div><div class="setting-list-description">{{ t('set.tgBackupTimeDesc', { zone: saved?.timeLocation || t('set.tgBackupServerZone') }) }}</div></div></div>
        <div class="acol"><div class="aspace-v">
          <div class="aselect"><select :value="backupTime.mode" :aria-label="t('set.tgBackupTime')" @change="setBackupTime({ mode: $event.target.value })">
            <option value="daily">{{ t('set.tgBackupMode.daily') }}</option>
            <option value="weekly">{{ t('set.tgBackupMode.weekly') }}</option>
            <option value="hours">{{ t('set.tgBackupMode.hours') }}</option>
            <option value="custom">{{ t('set.notifyTime.custom') }}</option>
          </select></div>
          <div v-if="backupTime.mode === 'hours'" class="acompact">
            <label class="ainput number"><input :value="backupTime.hours" type="number" min="1" max="720" class="ltr" :aria-label="t('set.notifyTime.interval')" @change="setBackupTime({ hours: $event.target.value })" /></label>
            <span class="abtn addon">{{ t('settings.hours') }}</span>
          </div>
          <div v-if="backupTime.mode === 'weekly'" class="aselect"><select :value="backupTime.day" :aria-label="t('set.tgBackupDay')" @change="setBackupTime({ day: Number($event.target.value) })">
            <option v-for="w in weekdays" :key="w.d" :value="w.d">{{ w.name }}</option>
          </select></div>
          <label v-if="backupTime.mode === 'daily' || backupTime.mode === 'weekly'" class="ainput"><input :value="backupTime.time" type="time" class="ltr" :aria-label="t('set.tgBackupAt')" @change="$event.target.value && setBackupTime({ time: $event.target.value })" /></label>
          <label v-if="backupTime.mode === 'custom'" class="ainput block"><input v-model="form[f.backupTime]" class="ltr" placeholder="0 0 3 * * *" /></label>
        </div></div>
      </div></div>
    </template>

    <template v-else>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.telegramNotifyTime') }}</div><div class="setting-list-description">{{ t(L.timeDesc) }}</div></div></div>
        <div class="acol"><div class="aspace-v">
          <div class="aselect"><select v-model="notifyMode" :aria-label="t('set.telegramNotifyTime')">
            <option value="every">{{ t('set.notifyTime.every') }}</option>
            <option value="@hourly">{{ t('set.notifyTime.hourly') }}</option>
            <option value="@daily">{{ t('set.notifyTime.daily') }}</option>
            <option value="@weekly">{{ t('set.notifyTime.weekly') }}</option>
            <option value="@monthly">{{ t('set.notifyTime.monthly') }}</option>
            <option value="custom">{{ t('set.notifyTime.custom') }}</option>
          </select></div>
          <div v-if="notifyMode === 'every'" class="acompact">
            <label class="ainput number"><input v-model.number="everyNum" type="number" min="1" class="ltr" :aria-label="t('set.notifyTime.interval')" /></label>
            <div class="aselect"><select v-model="everyUnit" :aria-label="t('set.notifyTime.unit')">
              <option value="s">{{ t('set.notifyTime.seconds') }}</option>
              <option value="m">{{ t('set.notifyTime.minutes') }}</option>
              <option value="h">{{ t('set.notifyTime.hours') }}</option>
            </select></div>
          </div>
          <label v-if="notifyMode === 'custom'" class="ainput block"><input v-model="form[f.runTime]" class="ltr" placeholder="0 30 8 * * *" /></label>
        </div></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t(L.events) }}</div><div class="setting-list-description">{{ t(L.eventsDesc) }}</div></div></div>
        <div class="acol">
          <div class="notif-grid">
            <div v-for="g in eventGroups" :key="g.title" class="acard small notif-card">
              <div class="acard-head small">
                <span class="notif-title"><AntIcon :name="g.icon" /> {{ t(g.title) }}</span>
                <span class="notif-extra"><span class="atag">{{ groupCount(f.kinds, g) }}/{{ g.events.length }}</span><input type="checkbox" class="acheck" :checked="groupCount(f.kinds, g) === g.events.length" :indeterminate.prop="groupCount(f.kinds, g) > 0 && groupCount(f.kinds, g) < g.events.length" @change="toggleGroup(f.kinds, g)" /></span>
              </div>
              <div class="acard-body"><div class="aspace-v">
                <div v-for="e in g.events" :key="e.key">
                  <label class="acheckbox"><input type="checkbox" :checked="hasKind(f.kinds, e.key)" @change="toggleKind(f.kinds, e.key)" />{{ t(e.label) }}</label>
                  <div v-if="e.setting && hasKind(f.kinds, e.key)" style="padding-left: 24px; margin-top: 4px">
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
