<script setup>
// The settings page's Subscription tab: where the subscription service
// listens, what customers' apps are told, and the look of the customer's page.
import { useSettings, sanitizePath } from './context.js'
import { api } from '../../lib/api.js'
import { t, notify } from '../../lib/store.js'
import AntIcon from '../AntIcon.vue'
import Toggle from '../Toggle.vue'

const { inner, sub, subError } = useSettings()

// The looks the customer's page can wear. Chosen here; every customer's
// page follows on its next opening.
const SUB_TEMPLATES = ['classic', 'aurora', 'waves', 'network', 'minimal', 'midnight', 'ember', 'ocean', 'forest', 'sunset', 'neon', 'glass', 'paper', 'terminal', 'carbon', 'royal', 'sakura', 'frost', 'graphite', 'mesh', 'retro']
// A one-time link, opened in a new tab: the page is served without the
// panel's session, so the tab cannot carry it.
async function previewTemplate(key) {
  try {
    const res = await api.post('/api/subscription/preview', { template: key })
    window.open(res.url, '_blank', 'noopener')
  } catch (e) {
    notify(e.message, 'error')
  }
}
</script>

<template>
    <div class="atabs-nav"><div class="atabs-list">
      <button class="atab" :class="{ active: inner.subscription === '1' }" @click="inner.subscription = '1'"><AntIcon name="SettingOutlined" /><span>{{ t('set.panelSettings') }}</span></button>
      <button class="atab" :class="{ active: inner.subscription === '2' }" @click="inner.subscription = '2'"><AntIcon name="InfoCircleOutlined" /><span>{{ t('set.information') }}</span></button>
      <button class="atab" :class="{ active: inner.subscription === '3' }" @click="inner.subscription = '3'"><AntIcon name="IdcardOutlined" /><span>{{ t('set.profile') }}</span></button>
      <button class="atab" :class="{ active: inner.subscription === '4' }" @click="inner.subscription = '4'"><AntIcon name="SafetyCertificateOutlined" /><span>{{ t('set.certs') }}</span></button>
      <button class="atab" :class="{ active: inner.subscription === '5' }" @click="inner.subscription = '5'"><AntIcon name="PictureOutlined" /><span>{{ t('set.subTemplate') }}</span></button>
    </div></div>

    <template v-if="inner.subscription === '1'">
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.subEnable') }}</div><div class="setting-list-description">{{ t('set.subEnableDesc') }}</div></div></div>
        <div class="acol"><Toggle v-model="sub.enabled" :label="t('set.subEnable')" /></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.subListen') }}</div><div class="setting-list-description">{{ t('set.subListenDesc') }}</div></div></div>
        <div class="acol"><label class="ainput block"><input v-model="sub.listen" class="ltr" /></label><p v-if="subError.listen" class="field-error">{{ subError.listen }}</p></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.subDomain') }}</div><div class="setting-list-description">{{ t('set.subDomainDesc') }}</div></div></div>
        <div class="acol"><label class="ainput block"><input v-model="sub.host" class="ltr" /></label></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.subPort') }}<span v-if="!sub.port" class="atag">{{ t('set.defaultTag') }}</span></div><div class="setting-list-description">{{ t('set.subPortDesc') }}</div></div></div>
        <div class="acol"><label class="ainput number"><input v-model.number="sub.port" type="number" min="0" max="65535" class="ltr" /></label><p v-if="subError.port" class="field-error">{{ subError.port }}</p></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.subPath') }}</div><div class="setting-list-description">{{ t('set.subPathDesc') }}</div></div></div>
        <div class="acol"><label class="ainput block"><input :value="sub.path" class="ltr" placeholder="/sub/" @input="sub.path = sanitizePath($event.target.value)" /></label><p v-if="subError.path" class="field-error">{{ subError.path }}</p></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.subURI') }}</div><div class="setting-list-description">{{ t('set.subURIDesc') }}</div></div></div>
        <div class="acol"><label class="ainput block"><input v-model="sub.reverseProxyUri" class="ltr" placeholder="(http|https)://domain[:port]/path/" /></label><p v-if="subError.reverseProxyUri" class="field-error">{{ subError.reverseProxyUri }}</p></div>
      </div></div>
      <!-- Addresses the service had before, still answered so links
           already handed out keep working. Added on their own when
           the port, the certificate or the path changes. -->
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.subFormer') }}</div><div class="setting-list-description">{{ t('set.subFormerDesc') }}</div></div></div>
        <div class="acol">
          <p v-if="!(sub.former || []).length" class="setting-list-description">{{ t('set.subFormerNone') }}</p>
          <div v-for="(f, i) in sub.former || []" :key="i" class="sub-former-row">
            <label class="ainput number"><input v-model.number="f.port" type="number" min="0" max="65535" class="ltr" :aria-label="t('set.subPort')" /></label>
            <label class="acheckbox"><input v-model="f.tls" type="checkbox" :disabled="!f.port" /><span>HTTPS</span></label>
            <label class="ainput block"><input :value="f.path" class="ltr" placeholder="/sub/" :aria-label="t('set.subPath')" @input="f.path = sanitizePath($event.target.value)" /></label>
            <span v-if="!f.port" class="atag">{{ t('set.subFormerPanel') }}</span>
            <button type="button" class="abtn small danger" :aria-label="t('action.delete')" @click="sub.former.splice(i, 1)"><AntIcon name="DeleteOutlined" /></button>
          </div>
          <button type="button" class="abtn small" @click="(sub.former ||= []).push({ port: 0, tls: false, path: sub.path })"><AntIcon name="PlusOutlined" /><span>{{ t('set.subFormerAdd') }}</span></button>
          <p v-if="subError.former" class="field-error">{{ subError.former }}</p>
        </div>
      </div></div>
    </template>

    <template v-else-if="inner.subscription === '2'">
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.subEncrypt') }}</div><div class="setting-list-description">{{ t('set.subEncryptDescWG') }}</div></div></div>
        <div class="acol"><Toggle v-model="sub.encode" :label="t('set.subEncrypt')" /></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.subUpdates') }}<span v-if="sub.updateHours === 12" class="atag">{{ t('set.defaultTag') }}</span></div><div class="setting-list-description">{{ t('set.subUpdatesDesc') }}</div></div></div>
        <div class="acol"><label class="ainput number"><input v-model.number="sub.updateHours" type="number" min="1" max="168" class="ltr" /></label><p v-if="subError.updateHours" class="field-error">{{ subError.updateHours }}</p></div>
      </div></div>
    </template>

    <template v-else-if="inner.subscription === '3'">
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.subTitle') }}</div><div class="setting-list-description">{{ t('set.subTitleDesc') }}</div></div></div>
        <div class="acol"><label class="ainput block"><input v-model="sub.title" /></label><p v-if="subError.title" class="field-error">{{ subError.title }}</p></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.subSupportUrl') }}</div><div class="setting-list-description">{{ t('set.subSupportUrlDesc') }}</div></div></div>
        <div class="acol"><label class="ainput block"><input v-model="sub.supportUrl" class="ltr" placeholder="https://example.com" /></label><p v-if="subError.supportUrl" class="field-error">{{ subError.supportUrl }}</p></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.subProfileUrl') }}</div><div class="setting-list-description">{{ t('set.subProfileUrlDesc') }}</div></div></div>
        <div class="acol"><label class="ainput block"><input v-model="sub.profileUrl" class="ltr" placeholder="https://example.com" /></label><p v-if="subError.profileUrl" class="field-error">{{ subError.profileUrl }}</p></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.subAnnounce') }}</div><div class="setting-list-description">{{ t('set.subAnnounceDesc') }}</div></div></div>
        <div class="acol"><label class="ainput block area"><textarea v-model="sub.announce" rows="3"></textarea></label><p v-if="subError.announce" class="field-error">{{ subError.announce }}</p></div>
      </div></div>
    </template>

    <template v-else-if="inner.subscription === '5'">
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.subTemplate') }}</div><div class="setting-list-description">{{ t('set.subTemplateDesc') }}</div></div></div>
        <div class="acol"></div>
      </div></div>
      <div class="tpl-grid">
        <label v-for="tp in SUB_TEMPLATES" :key="tp" class="tpl-card" :class="{ on: sub.template === tp }">
          <input v-model="sub.template" type="radio" name="sub-template" :value="tp" class="tpl-radio" />
          <div class="tpl-swatch" :class="tp">
            <span class="tpl-sw-head"></span>
            <span class="tpl-sw-row"></span><span class="tpl-sw-row short"></span>
            <span class="tpl-sw-bar"></span>
          </div>
          <div class="tpl-meta">
            <div class="tpl-name">{{ t(`set.subTpl.${tp}`) }}<span v-if="tp === 'classic'" class="atag">{{ t('set.defaultTag') }}</span></div>
            <div class="tpl-desc">{{ t(`set.subTpl.${tp}Desc`) }}</div>
            <button type="button" class="abtn small" @click.prevent="previewTemplate(tp)"><AntIcon name="EyeOutlined" /><span>{{ t('set.subTplPreview') }}</span></button>
          </div>
        </label>
      </div>
    </template>

    <template v-else>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.subCertPath') }}</div><div class="setting-list-description">{{ t('set.subCertPathDesc') }}</div></div></div>
        <div class="acol"><label class="ainput block"><input v-model="sub.certFile" class="ltr" /></label></div>
      </div></div>
      <div class="setting-list-item"><div class="arow">
        <div class="acol"><div class="setting-list-meta"><div class="setting-list-title">{{ t('set.subKeyPath') }}</div><div class="setting-list-description">{{ t('set.subKeyPathDesc') }}</div></div></div>
        <div class="acol"><label class="ainput block"><input v-model="sub.keyFile" class="ltr" /></label><p v-if="subError.certFile" class="field-error">{{ subError.certFile }}</p></div>
      </div></div>
    </template>
</template>
