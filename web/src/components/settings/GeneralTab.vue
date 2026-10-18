<script setup>
// The settings page's General tab: the panel itself, notifications'
// thresholds, certificates, outside traffic, date and time, and what a new
// customer starts with.
import { computed } from 'vue'
import { useSettings, sanitizePath } from './context.js'
import { t } from '../../lib/store.js'
import AntIcon from '../AntIcon.vue'
import Toggle from '../Toggle.vue'

const { form, inner, isDefault, outboundTags, balancerTags } = useSettings()

// Quota and rate are stored in bytes and bits. Nobody types either.
const quotaGB = computed({
  get: () => (form.value?.defaultQuotaBytes ? form.value.defaultQuotaBytes / 1024 ** 3 : 0),
  set: (v) => {
    form.value.defaultQuotaBytes = Math.max(0, Math.round(Number(v) * 1024 ** 3)) || 0
  },
})
const rateMbit = computed({
  get: () => (form.value?.defaultRateBitsPerSec ? form.value.defaultRateBitsPerSec / 1e6 : 0),
  set: (v) => {
    form.value.defaultRateBitsPerSec = Math.max(0, Math.round(Number(v) * 1e6)) || 0
  },
})
</script>

<template>
    <div class="atabs-nav"><div class="atabs-list">
      <button class="atab" :class="{ active: inner.general === '1' }" @click="inner.general = '1'"><AntIcon name="SettingOutlined" /><span>{{ t('set.panelSettings') }}</span></button>
      <button class="atab" :class="{ active: inner.general === '2' }" @click="inner.general = '2'"><AntIcon name="BellOutlined" /><span>{{ t('set.notifications') }}</span></button>
      <button class="atab" :class="{ active: inner.general === '3' }" @click="inner.general = '3'"><AntIcon name="SafetyCertificateOutlined" /><span>{{ t('set.certs') }}</span></button>
      <button class="atab" :class="{ active: inner.general === '4' }" @click="inner.general = '4'"><AntIcon name="GlobalOutlined" /><span>{{ t('set.externalTraffic') }}</span></button>
      <button class="atab" :class="{ active: inner.general === '5' }" @click="inner.general = '5'"><AntIcon name="ClockCircleOutlined" /><span>{{ t('set.dateAndTime') }}</span></button>
      <button class="atab" :class="{ active: inner.general === '6' }" @click="inner.general = '6'"><AntIcon name="TeamOutlined" /><span>{{ t('set.customerDefaults') }}</span></button>
    </div></div>

    <template v-if="inner.general === '1'">
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.panelListeningIP') }}</div><div class="setting-list-description">{{ t('set.panelListeningIPDesc') }}</div></div></div>
        <div class="acol"><label class="ainput block"><input v-model="form.webListen" class="ltr" /></label></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.panelListeningDomain') }}</div><div class="setting-list-description">{{ t('set.panelListeningDomainDesc') }}</div></div></div>
        <div class="acol"><label class="ainput block"><input v-model="form.webDomain" class="ltr" /></label></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.panelPort') }}<span v-if="isDefault('webPort')" class="atag">{{ t('set.defaultTag') }}</span></div><div class="setting-list-description">{{ t('set.panelPortDesc') }}</div></div></div>
        <div class="acol"><label class="ainput number"><input v-model.number="form.webPort" type="number" min="1" max="65535" class="ltr" /></label></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.panelUrlPath') }}</div><div class="setting-list-description">{{ t('set.panelUrlPathDesc') }}</div></div></div>
        <div class="acol"><label class="ainput block"><input :value="form.webBasePath" class="ltr" @input="form.webBasePath = sanitizePath($event.target.value)" /></label></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.sessionMaxAge') }}<span v-if="isDefault('sessionMaxAge')" class="atag">{{ t('set.defaultTag') }}</span></div><div class="setting-list-description">{{ t('set.sessionMaxAgeDesc') }}</div></div></div>
        <div class="acol"><label class="ainput number"><input v-model.number="form.sessionMaxAge" type="number" min="60" max="525600" class="ltr" /></label></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.trustedProxyCidrs') }}</div><div class="setting-list-description">{{ t('set.trustedProxyCidrsDesc') }}</div></div></div>
        <div class="acol"><label class="ainput block"><input v-model="form.trustedProxyCIDRs" class="ltr" placeholder="127.0.0.1/32,::1/128" /></label></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.panelOutbound') }}</div><div class="setting-list-description">{{ t('set.panelOutboundDesc') }}</div></div></div>
        <div class="acol"><div class="aselect"><select v-model="form.panelOutbound" :aria-label="t('set.panelOutbound')">
          <option value="">{{ t('set.panelOutboundPh') }}</option>
          <optgroup v-if="balancerTags.length" :label="t('nav.outbounds')"><option v-for="tag in outboundTags" :key="tag" :value="tag">{{ tag }}</option></optgroup>
          <template v-else><option v-for="tag in outboundTags" :key="tag" :value="tag">{{ tag }}</option></template>
          <optgroup v-if="balancerTags.length" :label="t('routing.tab.balancers')"><option v-for="tag in balancerTags" :key="tag" :value="tag">{{ tag }}</option></optgroup>
        </select></div></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.pageSize') }}<span v-if="isDefault('pageSize')" class="atag">{{ t('set.defaultTag') }}</span></div><div class="setting-list-description">{{ t('set.pageSizeDesc') }}</div></div></div>
        <div class="acol"><label class="ainput number"><input v-model.number="form.pageSize" type="number" min="0" max="1000" step="5" class="ltr" /></label></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.language') }}</div></div></div>
        <div class="acol"><div class="aselect"><select v-model="form.defaultLocale" :aria-label="t('set.language')">
          <option value="en">🇬🇧&nbsp;&nbsp;English</option>
          <option value="fa">🇮🇷&nbsp;&nbsp;فارسی</option>
        </select></div></div>
      </div></div>
    </template>

    <template v-else-if="inner.general === '2'">
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.expireTimeDiff') }}<span v-if="isDefault('expireDiff')" class="atag">{{ t('set.defaultTag') }}</span></div><div class="setting-list-description">{{ t('set.expireTimeDiffDesc') }}</div></div></div>
        <div class="acol"><label class="ainput number"><input v-model.number="form.expireDiff" type="number" min="0" class="ltr" /></label></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.trafficDiff') }}<span v-if="isDefault('trafficDiff')" class="atag">{{ t('set.defaultTag') }}</span></div><div class="setting-list-description">{{ t('set.trafficDiffDesc') }}</div></div></div>
        <div class="acol"><label class="ainput number"><input v-model.number="form.trafficDiff" type="number" min="0" max="100" class="ltr" /></label></div>
      </div></div>
    </template>

    <template v-else-if="inner.general === '3'">
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.publicKeyPath') }}</div><div class="setting-list-description">{{ t('set.publicKeyPathDesc') }}</div></div></div>
        <div class="acol"><label class="ainput block"><input v-model="form.webCertFile" class="ltr" /></label></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.privateKeyPath') }}</div><div class="setting-list-description">{{ t('set.privateKeyPathDesc') }}</div></div></div>
        <div class="acol"><label class="ainput block"><input v-model="form.webKeyFile" class="ltr" /></label></div>
      </div></div>
    </template>

    <template v-else-if="inner.general === '4'">
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.externalTrafficInformEnable') }}</div><div class="setting-list-description">{{ t('set.externalTrafficInformEnableDesc') }}</div></div></div>
        <div class="acol"><Toggle v-model="form.externalTrafficInformEnable" :label="t('set.externalTrafficInformEnable')" /></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.externalTrafficInformURI') }}</div><div class="setting-list-description">{{ t('set.externalTrafficInformURIDesc') }} {{ t('set.informBody') }}</div></div></div>
        <div class="acol"><label class="ainput block"><input v-model="form.externalTrafficInformURI" class="ltr" placeholder="(http|https)://domain[:port]/path/" /></label></div>
      </div></div>
    </template>

    <template v-else-if="inner.general === '5'">
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.timeZone') }}</div><div class="setting-list-description">{{ t('set.timeZoneDesc') }}</div></div></div>
        <div class="acol"><label class="ainput block"><input v-model="form.timeLocation" class="ltr" placeholder="Asia/Tehran" /></label></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.datepicker') }}</div><div class="setting-list-description">{{ t('set.datepickerDescription') }}</div></div></div>
        <div class="acol"><div class="aselect"><select v-model="form.datepicker" :aria-label="t('set.datepicker')">
          <option value="gregorian">{{ t('set.calendarGregorian') }}</option>
          <option value="jalalian">{{ t('set.calendarJalalian') }}</option>
        </select></div></div>
      </div></div>
    </template>

    <template v-else>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.defQuota') }}<span v-if="isDefault('defaultQuotaBytes')" class="atag">{{ t('set.defaultTag') }}</span></div><div class="setting-list-description">{{ t('set.defQuotaDesc') }}</div></div></div>
        <div class="acol"><div class="acompact"><label class="ainput number"><input v-model.number="quotaGB" type="number" min="0" class="ltr" /></label><span class="abtn addon">GB</span></div></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.defExpiry') }}<span v-if="isDefault('defaultExpiryDays')" class="atag">{{ t('set.defaultTag') }}</span></div><div class="setting-list-description">{{ t('set.defExpiryDesc') }}</div></div></div>
        <div class="acol"><div class="acompact"><label class="ainput number"><input v-model.number="form.defaultExpiryDays" type="number" min="0" class="ltr" /></label><span class="abtn addon">{{ t('settings.days') }}</span></div></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.defDevices') }}<span v-if="isDefault('defaultDeviceLimit')" class="atag">{{ t('set.defaultTag') }}</span></div><div class="setting-list-description">{{ t('set.defDevicesDesc') }}</div></div></div>
        <div class="acol"><label class="ainput number"><input v-model.number="form.defaultDeviceLimit" type="number" min="1" max="64" class="ltr" /></label></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.defRate') }}<span v-if="isDefault('defaultRateBitsPerSec')" class="atag">{{ t('set.defaultTag') }}</span></div><div class="setting-list-description">{{ t('set.defRateDesc') }}</div></div></div>
        <div class="acol"><div class="acompact"><label class="ainput number"><input v-model.number="rateMbit" type="number" min="0" class="ltr" /></label><span class="abtn addon">Mbit/s</span></div></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.defReset') }}<span v-if="isDefault('defaultResetCycle')" class="atag">{{ t('set.defaultTag') }}</span></div><div class="setting-list-description">{{ t('set.defResetDesc') }}</div></div></div>
        <div class="acol"><div class="aselect"><select v-model="form.defaultResetCycle" :aria-label="t('set.defReset')">
          <option value="none">{{ t('reset.none') }}</option>
          <option value="daily">{{ t('reset.daily') }}</option>
          <option value="weekly">{{ t('reset.weekly') }}</option>
          <option value="monthly">{{ t('reset.monthly') }}</option>
        </select></div></div>
      </div></div>
    </template>
</template>
