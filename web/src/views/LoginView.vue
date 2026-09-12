<script setup>
import { nextTick, ref, computed, onMounted, onUnmounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { store, t, signIn, loadMessages } from '../lib/store.js'
import AntIcon from '../components/AntIcon.vue'
import { themeMode, cycleTheme } from '../lib/theme.js'

const router = useRouter()
const route = useRoute()

const username = ref('admin')
const password = ref('')
const error = ref('')
const code = ref('')
const needCode = ref(false)
const busy = ref(false)
const langOpen = ref(false)
const showPw = ref(false)

// The headline cycles a word at a time, the way 3x-ui's does. It is the one
// piece of motion on an otherwise still screen, so it carries the page.
// Two words, turn and turn about, as 3x-ui's headline does.
const words = computed(() => [t('login.hello'), t('login.title')])
const wordIndex = ref(0)
const word = computed(() => words.value[wordIndex.value % words.value.length])
let cycle = null

onMounted(() => {
  const reduced = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
  if (!reduced) cycle = setInterval(() => wordIndex.value++, 2600)
})
onUnmounted(() => clearInterval(cycle))

async function submit() {
  error.value = ''
  busy.value = true
  try {
    const res = await signIn(username.value, password.value, code.value)
    if (res && res.needCode) {
      needCode.value = true
      // Focus lands on the code box so the operator can type straight away
      // rather than hunting for a field that has just appeared. nextTick
      // rather than an animation frame: a frame never arrives in a tab the
      // browser has backgrounded, and the field would stay unfocused.
      await nextTick()
      document.getElementById('login-code')?.focus()
      return
    }
    router.replace(route.query.next || '/')
  } catch (err) {
    error.value = err.message
  } finally {
    busy.value = false
  }
}

async function pickLocale(l) {
  langOpen.value = false
  await loadMessages(l)
}

const locales = computed(() => store.meta?.locales || ['en'])
const localeName = (l) => (l === 'fa' ? 'فارسی' : 'English')
const localeIcon = (l) => (l === 'fa' ? '🇮🇷' : '🇬🇧')
</script>

<template>
  <div class="login-app" :class="{ 'is-dark': themeMode !== 'light', 'is-ultra': themeMode === 'ultra' }">
    <div class="login-content">
      <!-- Their toolbar: two 40px round buttons, fixed top-right. -->
      <div class="login-toolbar">
        <button class="abtn toolbar-btn" type="button" :title="t('nav.theme')" :aria-label="t('nav.theme')" @click="cycleTheme">
          <AntIcon :name="themeMode === 'light' ? 'SunOutlined' : themeMode === 'dark' ? 'MoonOutlined' : 'MoonFilled'" />
        </button>
        <div class="lang">
          <button class="abtn toolbar-btn" type="button" :aria-label="t('settings.language')" :aria-expanded="langOpen" @click="langOpen = !langOpen">
            <AntIcon name="TranslationOutlined" />
          </button>
          <div v-if="langOpen" class="amenu lang-menu" role="menu">
            <button v-for="l in locales" :key="l" class="amenu-item" :class="{ selected: l === store.locale }" role="menuitem" @click="pickLocale(l)">
              <span aria-hidden="true">{{ localeIcon(l) }}</span><span>{{ localeName(l) }}</span>
            </button>
          </div>
        </div>
      </div>

      <div class="login-wrapper">
        <div class="login-card">
          <div class="brand">
            <span class="brand-name">W-UI</span>
            <span class="brand-accent" aria-hidden="true"></span>
          </div>
          <h2 class="welcome"><b :key="word">{{ word }}</b></h2>

          <form class="login-form" @submit.prevent="submit">
            <div class="aform-item">
              <label class="aform-label" for="login-user">{{ t('auth.username') }}</label>
              <label class="ainput block large">
                <span class="ainput-prefix"><AntIcon name="UserOutlined" /></span>
                <input id="login-user" v-model="username" autocomplete="username" :placeholder="t('auth.username')" autofocus required />
              </label>
            </div>
            <div class="aform-item">
              <label class="aform-label" for="login-pass">{{ t('auth.password') }}</label>
              <label class="ainput block large">
                <span class="ainput-prefix"><AntIcon name="LockOutlined" /></span>
                <input id="login-pass" v-model="password" :type="showPw ? 'text' : 'password'" autocomplete="current-password" :placeholder="t('auth.password')" required />
                <button type="button" class="ainput-eye" :aria-label="t('auth.password')" @click="showPw = !showPw"><AntIcon :name="showPw ? 'EyeOutlined' : 'EyeInvisibleOutlined'" /></button>
              </label>
            </div>
            <!-- Only after the password was accepted, so this reveals nothing
                 to someone guessing at the username. -->
            <div v-if="needCode" class="aform-item">
              <label class="aform-label" for="login-code">{{ t('auth.code') }}</label>
              <label class="ainput block large">
                <span class="ainput-prefix"><AntIcon name="KeyOutlined" /></span>
                <input id="login-code" v-model="code" inputmode="numeric" autocomplete="one-time-code" maxlength="6" :placeholder="t('auth.code')" class="ltr" required />
              </label>
            </div>

            <p v-if="error" class="error" role="alert">{{ error }}</p>

            <div class="aform-item submit-row">
              <button class="abtn primary large block" type="submit" :disabled="busy">
                <span v-if="busy" class="spin"></span>
                <template v-else>{{ t('auth.signIn') }}</template>
              </button>
            </div>
          </form>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* 3x-ui's LoginPage.css, in this panel's colours: five blurred blobs and a
   fading grid behind a frosted card, the name in a gradient, the headline
   turning a word at a time, Ant's large inputs and button. */
.login-app {
  --bg-card: rgba(20, 18, 21, 0.55);
  --bg-card-solid: #141215;
  --color-border: rgba(255, 255, 255, 0.1);
  --shadow-card: 0 1px 3px rgba(0, 0, 0, 0.4), 0 20px 60px rgba(224, 46, 61, 0.22);
  --blob-1: rgba(224, 46, 61, 0.42);
  --blob-2: rgba(160, 26, 42, 0.42);
  --blob-3: rgba(242, 64, 79, 0.3);
  --blob-4: rgba(251, 146, 60, 0.14);
  --blob-5: rgba(120, 18, 30, 0.35);
  --grid-color: rgba(255, 255, 255, 0.04);
  --hair-a: rgba(255, 255, 255, 0.15);
  --hair-b: rgba(224, 46, 61, 0.4);
  position: relative;
  min-height: 100vh;
  overflow: hidden;
  color: var(--ink);
  background: radial-gradient(ellipse at 25% 20%, #1a0d11 0%, #0a090a 60%);
}
.login-app:not(.is-dark) {
  --bg-card: rgba(255, 255, 255, 0.72);
  --bg-card-solid: #ffffff;
  --color-border: rgba(255, 255, 255, 0.6);
  --shadow-card: 0 1px 3px rgba(0, 0, 0, 0.04), 0 18px 50px rgba(200, 31, 46, 0.18);
  --blob-1: rgba(200, 31, 46, 0.35);
  --blob-2: rgba(242, 64, 79, 0.3);
  --blob-3: rgba(251, 146, 60, 0.25);
  --blob-4: rgba(160, 26, 42, 0.2);
  --blob-5: rgba(200, 31, 46, 0.2);
  --grid-color: rgba(200, 31, 46, 0.06);
  --hair-a: rgba(255, 255, 255, 0.5);
  --hair-b: rgba(200, 31, 46, 0.25);
  background: linear-gradient(135deg, #fbe9eb 0%, #fdf2f8 50%, #fff5f5 100%);
}
.login-app.is-ultra {
  --bg-card: rgba(10, 10, 12, 0.6);
  --bg-card-solid: #0a0a0c;
  --color-border: rgba(255, 255, 255, 0.06);
  --blob-1: rgba(224, 46, 61, 0.3);
  --blob-2: rgba(160, 26, 42, 0.22);
  --blob-3: rgba(242, 64, 79, 0.2);
  --blob-4: rgba(251, 146, 60, 0.12);
  --blob-5: rgba(120, 18, 30, 0.25);
  --grid-color: rgba(255, 255, 255, 0.025);
  background: radial-gradient(ellipse at 25% 20%, #150709 0%, #000 60%);
}

.login-app::before, .login-app::after, .login-content::before, .login-content::after, .login-wrapper::before {
  content: ''; position: absolute; border-radius: 50%; pointer-events: none; z-index: 0; will-change: transform;
}
.login-app::before { top: -25vw; left: -20vw; width: 70vw; height: 70vw; max-width: 900px; max-height: 900px; filter: blur(100px); background: radial-gradient(circle, var(--blob-1) 0%, transparent 65%); animation: blob-drift-a 24s ease-in-out infinite alternate; }
.login-app::after { bottom: -25vw; right: -20vw; width: 70vw; height: 70vw; max-width: 900px; max-height: 900px; filter: blur(100px); background: radial-gradient(circle, var(--blob-2) 0%, transparent 65%); animation: blob-drift-b 30s ease-in-out infinite alternate; }
.login-content::before { top: 30%; left: 50%; width: 50vw; height: 50vw; max-width: 700px; max-height: 700px; filter: blur(100px); background: radial-gradient(circle, var(--blob-3) 0%, transparent 65%); animation: blob-drift-c 36s ease-in-out infinite alternate; }
.login-content::after { top: 10%; right: 15%; width: 35vw; height: 35vw; max-width: 500px; max-height: 500px; filter: blur(90px); background: radial-gradient(circle, var(--blob-4) 0%, transparent 65%); animation: blob-drift-d 28s ease-in-out infinite alternate; }
.login-wrapper::before { bottom: 5%; left: 10%; width: 35vw; height: 35vw; max-width: 500px; max-height: 500px; filter: blur(90px); background: radial-gradient(circle, var(--blob-5) 0%, transparent 65%); animation: blob-drift-e 32s ease-in-out infinite alternate; }
.login-wrapper::after {
  content: ''; position: absolute; inset: 0; pointer-events: none; z-index: 0;
  background-image: linear-gradient(var(--grid-color) 1px, transparent 1px), linear-gradient(90deg, var(--grid-color) 1px, transparent 1px);
  background-size: 48px 48px; background-position: center;
  -webkit-mask-image: radial-gradient(ellipse at center, black 30%, transparent 75%); mask-image: radial-gradient(ellipse at center, black 30%, transparent 75%);
}
@keyframes blob-drift-a { 0% { transform: translate(0, 0) scale(1); } 50% { transform: translate(18vw, 10vh) scale(1.15); } 100% { transform: translate(34vw, 22vh) scale(1.25); } }
@keyframes blob-drift-b { 0% { transform: translate(0, 0) scale(1); } 50% { transform: translate(-16vw, -10vh) scale(1.12); } 100% { transform: translate(-30vw, -22vh) scale(1.2); } }
@keyframes blob-drift-c { 0% { transform: translate(-50%, -50%) scale(1); } 50% { transform: translate(-20%, -20%) scale(1.1); } 100% { transform: translate(-80%, -10%) scale(1.05); } }
@keyframes blob-drift-d { 0% { transform: translate(0, 0) scale(0.9); } 50% { transform: translate(-12vw, 14vh) scale(1.05); } 100% { transform: translate(8vw, -8vh) scale(1.1); } }
@keyframes blob-drift-e { 0% { transform: translate(0, 0) scale(1); } 50% { transform: translate(14vw, -8vh) scale(1.1); } 100% { transform: translate(-6vw, 12vh) scale(1.15); } }
@media (prefers-reduced-motion: reduce) {
  .login-app::before, .login-app::after, .login-content::before, .login-content::after, .login-wrapper::before { animation: none; }
  .welcome b { animation: none; }
}

.login-content { position: relative; }
.login-content > * { position: relative; z-index: 1; }
.login-toolbar { position: fixed; top: 16px; right: 16px; z-index: 10; display: inline-flex; align-items: center; gap: 8px; }
.toolbar-btn { width: 40px; height: 40px; min-width: 40px; padding: 0; border-radius: 50%; background: var(--bg-card-solid); }
.toolbar-btn .anticon { font-size: 18px; }
.lang { position: relative; }
.lang-menu { position: absolute; top: calc(100% + 4px); inset-inline-end: 0; min-width: 160px; }
.lang-menu .amenu-item.selected { color: var(--accent); background: var(--accent-soft); }

.login-wrapper { position: relative; min-height: 100vh; display: flex; align-items: center; justify-content: center; padding: 24px 16px; }
.login-card {
  position: relative; z-index: 2; width: 100%; max-width: 400px; padding: 40px 32px 28px;
  background: var(--bg-card); border: 1px solid var(--color-border); border-radius: 20px; box-shadow: var(--shadow-card);
  -webkit-backdrop-filter: blur(24px) saturate(180%); backdrop-filter: blur(24px) saturate(180%);
}
.login-card::before {
  content: ''; position: absolute; inset: 0; border-radius: 20px; padding: 1px; pointer-events: none;
  background: linear-gradient(135deg, var(--hair-a), rgba(255, 255, 255, 0) 40%, var(--hair-b) 80%);
  -webkit-mask: linear-gradient(#000 0 0) content-box, linear-gradient(#000 0 0); -webkit-mask-composite: xor; mask-composite: exclude;
}
@media (max-width: 480px) { .login-card { padding: 32px 20px 24px; } }

.brand { display: flex; flex-direction: column; align-items: center; gap: 10px; margin-bottom: 8px; }
.brand-name {
  font-size: 28px; font-weight: 700; letter-spacing: 1.5px;
  background: linear-gradient(135deg, var(--accent-hover), var(--accent)); -webkit-background-clip: text; background-clip: text; -webkit-text-fill-color: transparent;
}
.brand-accent { display: block; width: 40px; height: 3px; border-radius: 2px; background: linear-gradient(90deg, var(--accent-hover), var(--accent)); }
.welcome { min-height: 42px; margin: 12px 0 28px; text-align: center; color: var(--ink); font-size: 32px; font-weight: 700; line-height: 1.2; letter-spacing: 0.3px; }
.welcome b { display: inline-block; font-weight: inherit; animation: headline-in 280ms ease both; }
@keyframes headline-in { 0% { opacity: 0; transform: translateY(6px); } 100% { opacity: 1; transform: translateY(0); } }

/* Ant's Form layout="vertical" with size="large" controls. */
.login-form .aform-label { font-weight: 500; color: var(--ink); }
.login-form .ainput.large { height: 40px; padding: 7px 11px; border-radius: 8px; font-size: 16px; background: var(--bg-card-solid); }
.login-form .ainput.large input { font-size: 16px; }
.login-form .ainput-prefix { color: var(--ink); }
.ainput-eye { display: inline-flex; padding: 0; border: 0; background: none; color: var(--faint); font-size: 14px; cursor: pointer; }
.ainput-eye:hover { color: var(--ink); }
.login-form input:-webkit-autofill, .login-form input:-webkit-autofill:hover, .login-form input:-webkit-autofill:focus {
  -webkit-text-fill-color: var(--ink) !important; -webkit-box-shadow: 0 0 0 1000px var(--bg-card-solid) inset !important; box-shadow: 0 0 0 1000px var(--bg-card-solid) inset !important;
  transition: background-color 9999s ease-in-out 0s, color 9999s ease-in-out 0s;
}
.abtn.large { height: 40px; padding: 6px 15px; border-radius: 8px; font-size: 16px; }
.abtn.block { width: 100%; }
.submit-row { margin-bottom: 0; }
.error { margin: -8px 0 16px; color: var(--bad); font-size: 13px; }
.spin { width: 16px; height: 16px; border: 2px solid rgba(255,255,255,.35); border-top-color: #fff; border-radius: 50%; animation: rot 0.8s linear infinite; }
@keyframes rot { to { transform: rotate(360deg); } }
</style>
