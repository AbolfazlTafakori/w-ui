<script setup>
// The engine's basics: the general settings, statistics, connection limits,
// logging, and going back to the defaults -- each on an inner tab.
import { t } from '../../lib/store.js'
import { useEngine } from './context.js'
import AntIcon from '../AntIcon.vue'
import Toggle from '../Toggle.vue'

defineProps({ busy: { type: Boolean, default: false } })
defineEmits(['reset'])

const { form, inner, isDefault } = useEngine()
</script>

<template>
<div class="atabs-nav"><div class="atabs-list">
  <button class="atab" :class="{ active: inner.basic === '1' }" @click="inner.basic = '1'"><AntIcon name="SettingOutlined" /><span>{{ t('eng.generalConfigs') }}</span></button>
  <button class="atab" :class="{ active: inner.basic === '2' }" @click="inner.basic = '2'"><AntIcon name="BarChartOutlined" /><span>{{ t('eng.statistics') }}</span></button>
  <button class="atab" :class="{ active: inner.basic === '3' }" @click="inner.basic = '3'"><AntIcon name="ClockCircleOutlined" /><span>{{ t('eng.connectionLimits') }}</span></button>
  <button class="atab" :class="{ active: inner.basic === '4' }" @click="inner.basic = '4'"><AntIcon name="FileTextOutlined" /><span>{{ t('eng.logConfigs') }}</span></button>
  <button class="atab" :class="{ active: inner.basic === '5' }" @click="inner.basic = '5'"><AntIcon name="ReloadOutlined" /><span>{{ t('eng.resetDefaultConfig') }}</span></button>
</div></div>

<template v-if="inner.basic === '1'">
  <div class="aalert warning mb-12 hint-alert">
    <AntIcon name="ExclamationCircleFilled" />
    <div class="aalert-body"><span class="aalert-title">{{ t('eng.generalConfigsDesc') }}</span></div>
  </div>
  <div class="setting-list-item"><div class="arow">
    <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.routingStrategy') }}</div><div class="setting-list-description">{{ t('eng.routingStrategyDesc') }}</div></div></div>
    <div class="acol"><div class="aselect"><select v-model="form.domainStrategy">
      <option value="AsIs">AsIs</option>
      <option value="UseIPv4">UseIPv4</option>
      <option value="UseIPv6">UseIPv6</option>
    </select></div></div>
  </div></div>
  <div class="setting-list-item"><div class="arow">
    <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.outboundTestUrl') }}</div><div class="setting-list-description">{{ t('eng.outboundTestUrlDesc') }}</div></div></div>
    <div class="acol"><label class="ainput block"><input v-model="form.outboundTestUrl" class="ltr" placeholder="https://www.cloudflare.com/cdn-cgi/trace" /></label></div>
  </div></div>
</template>

<template v-else-if="inner.basic === '2'">
  <div class="setting-list-item"><div class="arow">
    <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.statsOutbound') }}</div><div class="setting-list-description">{{ t('eng.statsOutboundDesc') }}</div></div></div>
    <div class="acol"><Toggle v-model="form.outboundCounters" :label="t('eng.statsOutbound')" /></div>
  </div></div>
  <div class="setting-list-item"><div class="arow">
    <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.collectInterval') }}<span v-if="isDefault('collectInterval')" class="atag">{{ t('set.defaultTag') }}</span></div><div class="setting-list-description">{{ t('eng.collectIntervalDesc') }}</div></div></div>
    <div class="acol"><label class="ainput number"><input v-model.number="form.collectInterval" type="number" min="0" max="3600" class="ltr" placeholder="0" /><span class="ainput-suffix">{{ t('eng.seconds') }}</span></label></div>
  </div></div>
  <div class="setting-list-item"><div class="arow">
    <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.onlineWindow') }}<span v-if="isDefault('onlineWindow')" class="atag">{{ t('set.defaultTag') }}</span></div><div class="setting-list-description">{{ t('eng.onlineWindowDesc') }}</div></div></div>
    <div class="acol"><label class="ainput number"><input v-model.number="form.onlineWindow" type="number" min="10" max="86400" class="ltr" /><span class="ainput-suffix">{{ t('eng.seconds') }}</span></label></div>
  </div></div>
</template>

<template v-else-if="inner.basic === '3'">
  <div class="aalert warning mb-12 hint-alert">
    <AntIcon name="ExclamationCircleFilled" />
    <div class="aalert-body"><span class="aalert-title">{{ t('eng.connectionLimitsDesc') }}</span></div>
  </div>
  <div class="setting-list-item"><div class="arow">
    <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.probeInterval') }}<span v-if="isDefault('probeInterval')" class="atag">{{ t('set.defaultTag') }}</span></div><div class="setting-list-description">{{ t('eng.probeIntervalDesc') }}</div></div></div>
    <div class="acol"><label class="ainput number"><input v-model.number="form.probeInterval" type="number" min="10" max="86400" class="ltr" placeholder="60" /><span class="ainput-suffix">{{ t('eng.seconds') }}</span></label></div>
  </div></div>
  <div class="setting-list-item"><div class="arow">
    <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.enableConcurrency') }}</div><div class="setting-list-description">{{ t('eng.enableConcurrencyDesc') }}</div></div></div>
    <div class="acol"><Toggle v-model="form.probeConcurrency" :label="t('eng.enableConcurrency')" /></div>
  </div></div>
</template>

<template v-else-if="inner.basic === '4'">
  <div class="aalert warning mb-12 hint-alert">
    <AntIcon name="ExclamationCircleFilled" />
    <div class="aalert-body"><span class="aalert-title">{{ t('eng.logConfigsDesc') }}</span></div>
  </div>
  <div class="setting-list-item"><div class="arow">
    <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.logLevel') }}</div><div class="setting-list-description">{{ t('eng.logLevelDesc') }}</div></div></div>
    <div class="acol"><div class="aselect"><select v-model="form.logLevel">
      <option value="">{{ t('eng.empty') }}</option>
      <option value="debug">debug</option>
      <option value="info">info</option>
      <option value="warn">warning</option>
      <option value="error">error</option>
    </select></div></div>
  </div></div>
  <div class="setting-list-item"><div class="arow">
    <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.logFormat') }}</div><div class="setting-list-description">{{ t('eng.logFormatDesc') }}</div></div></div>
    <div class="acol"><div class="aselect"><select v-model="form.logFormat">
      <option value="">{{ t('eng.empty') }}</option>
      <option value="text">text</option>
      <option value="json">json</option>
    </select></div></div>
  </div></div>
  <div class="setting-list-item"><div class="arow">
    <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.accessLog') }}</div><div class="setting-list-description">{{ t('eng.accessLogDesc') }}</div></div></div>
    <div class="acol"><Toggle v-model="form.accessLog" :label="t('eng.accessLog')" /></div>
  </div></div>
  <div class="setting-list-item"><div class="arow">
    <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('eng.maskAddress') }}</div><div class="setting-list-description">{{ t('eng.maskAddressDesc') }}</div></div></div>
    <div class="acol"><Toggle v-model="form.maskAddress" :label="t('eng.maskAddress')" /></div>
  </div></div>
</template>

<template v-else-if="inner.basic === '5'">
  <div class="aspace" style="padding: 0 20px">
    <button class="abtn primary danger-primary" :disabled="busy" @click="$emit('reset')"><AntIcon name="ReloadOutlined" /><span>{{ t('eng.resetDefaultConfig') }}</span></button>
  </div>
</template>
</template>
