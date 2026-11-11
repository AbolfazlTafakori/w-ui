<script setup>
// the classic panel's QrPanel: a bordered panel with the remark as a green tag, the
// copy / download-image / download buttons beside it, and the code drawn
// as an SVG on white below. Clicking the code copies it as an image.
import { ref, watch } from 'vue'
import QRCode from 'qrcode'
import { t, notify } from '../lib/store.js'
import AntIcon from './AntIcon.vue'

const props = defineProps({
  value: { type: String, required: true },
  remark: { type: String, default: '' },
  downloadName: { type: String, default: '' },
  size: { type: Number, default: 360 },
  showQr: { type: Boolean, default: true },
})

const svg = ref('')
const canvas = ref(null)

// A module must not fall below this many pixels on screen, or a phone camera
// cannot resolve it. A dense profile -- an AmneziaWG tunnel runs to a hundred
// modules a side once its obfuscation fields are in -- is therefore drawn
// larger than the caller's nominal size rather than crushed into it; the cap
// keeps even the densest code to something a dialog can hold.
const MIN_MODULE_PX = 4
const MAX_QR_PX = 480
const MARGIN_MODULES = 4

// The size the code is actually shown and exported at: the caller's size when
// the profile is short, grown toward a readable density when it is long.
const displaySize = ref(props.size)

async function draw() {
  if (!props.showQr || !props.value) {
    svg.value = ''
    return
  }
  try {
    const across = QRCode.create(props.value, { errorCorrectionLevel: 'L' }).modules.size + MARGIN_MODULES * 2
    displaySize.value = Math.min(MAX_QR_PX, Math.max(props.size, across * MIN_MODULE_PX))
    // Their QRCode: errorLevel L, four modules of margin, black on white.
    svg.value = await QRCode.toString(props.value, {
      type: 'svg',
      errorCorrectionLevel: 'L',
      margin: MARGIN_MODULES,
      color: { dark: '#000000', light: '#ffffff' },
    })
  } catch (e) {
    svg.value = ''
    notify(e.message, 'error')
  }
}
watch(() => [props.value, props.showQr], draw, { immediate: true })

async function copy() {
  try {
    await navigator.clipboard.writeText(props.value)
    notify(t('common.copied'), 'success')
  } catch {
    notify(t('action.copyFailed'), 'error')
  }
}

function downloadText() {
  if (!props.downloadName) return
  const url = URL.createObjectURL(new Blob([props.value], { type: 'text/plain;charset=utf-8' }))
  const a = document.createElement('a')
  a.href = url
  a.download = props.downloadName
  a.click()
  URL.revokeObjectURL(url)
}

function toPng() {
  const el = canvas.value?.querySelector('svg')
  if (!el) return Promise.resolve(null)
  const data = new XMLSerializer().serializeToString(el)
  const url = URL.createObjectURL(new Blob([data], { type: 'image/svg+xml;charset=utf-8' }))
  return new Promise((resolve) => {
    const img = new Image()
    img.onload = () => {
      const c = document.createElement('canvas')
      c.width = displaySize.value
      c.height = displaySize.value
      const ctx = c.getContext('2d')
      ctx.fillStyle = '#ffffff'
      ctx.fillRect(0, 0, displaySize.value, displaySize.value)
      ctx.drawImage(img, 0, 0, displaySize.value, displaySize.value)
      URL.revokeObjectURL(url)
      c.toBlob((b) => resolve(b), 'image/png')
    }
    img.onerror = () => {
      URL.revokeObjectURL(url)
      resolve(null)
    }
    img.src = url
  })
}

function saveBlob(blob) {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `${props.remark || 'qrcode'}.png`
  a.click()
  URL.revokeObjectURL(url)
}

async function copyImage() {
  const blob = await toPng()
  if (!blob) return
  try {
    await navigator.clipboard.write([new ClipboardItem({ 'image/png': blob })])
    notify(t('common.copied'), 'success')
  } catch {
    saveBlob(blob)
  }
}

async function downloadImage() {
  const blob = await toPng()
  if (blob) saveBlob(blob)
}
</script>

<template>
  <div class="qr-panel">
    <div class="qr-panel-header">
      <span class="atag green qr-remark">{{ remark }}</span>
      <button class="abtn small icon" :title="t('action.copy')" :aria-label="t('action.copy')" @click="copy"><AntIcon name="CopyOutlined" /></button>
      <button v-if="showQr" class="abtn small icon" :title="t('client.downloadImage')" :aria-label="t('client.downloadImage')" @click="downloadImage"><AntIcon name="PictureOutlined" /></button>
      <button v-if="downloadName" class="abtn small icon" :title="t('action.download')" :aria-label="t('action.download')" @click="downloadText"><AntIcon name="DownloadOutlined" /></button>
    </div>
    <div v-if="showQr" ref="canvas" class="qr-panel-canvas" role="button" tabindex="0" :aria-label="t('action.copy')" :title="t('action.copy')" @click="copyImage" @keydown.enter="copyImage">
      <div class="qr-code" :style="{ width: displaySize + 'px', maxWidth: '100%' }" v-html="svg"></div>
    </div>
  </div>
</template>

<style>
.qr-panel {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-bottom: 10px;
  padding: 10px;
  border: 1px solid var(--line-soft);
  border-radius: 8px;
}
.qr-panel-header { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
.qr-remark { margin: 0; }
.qr-panel-canvas { display: flex; justify-content: center; padding: 6px 0; }
.qr-panel-canvas .qr-code { cursor: pointer; background: #fff; border-radius: 4px; line-height: 0; }
.qr-panel-canvas .qr-code svg { display: block; width: 100%; height: auto; }
</style>
