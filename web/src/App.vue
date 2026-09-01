<script setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter, RouterLink, RouterView } from 'vue-router'
import { store, t, signOut, loadMessages } from './lib/store.js'
import Icon from './components/Icon.vue'

const router = useRouter()

const signedIn = computed(() => !!store.admin)

const nav = [
  { to: '/', key: 'nav.overview', icon: 'dashboard', exact: true },
  { to: '/clients', key: 'nav.clients', icon: 'users' },
  { to: '/groups', key: 'nav.groups', icon: 'tag' },
  { to: '/interfaces', key: 'nav.interfaces', icon: 'server' },
  { to: '/sharing', key: 'nav.sharing', icon: 'eye' },
  { to: '/settings', key: 'nav.settings', icon: 'settings' },
]

const REPO_URL = 'https://github.com/AbolfazlTafakori/w-ui'
const PINNED_KEY = 'wui.sidebar.pinned'

// The rail is narrow until pointed at. Pinning is a deliberate choice to give
// up the space permanently, so it has to survive a reload — an operator who
// pinned it does not want to pin it again on every visit.
const pinned = ref(false)
const hovered = ref(false)
const drawerOpen = ref(false)

onMounted(() => {
  try {
    pinned.value = localStorage.getItem(PINNED_KEY) === 'true'
  } catch {
    // Private windows and blocked site data both throw here. A rail that
    // forgets its state is a much smaller problem than a panel that will not
    // render, so this is deliberately swallowed.
  }
  document.addEventListener('keydown', onKeydown)
})

onBeforeUnmount(() => document.removeEventListener('keydown', onKeydown))

function togglePinned() {
  pinned.value = !pinned.value
  try {
    localStorage.setItem(PINNED_KEY, String(pinned.value))
  } catch {
    /* see above */
  }
}

const expanded = computed(() => pinned.value || hovered.value)

function onKeydown(event) {
  if (event.key === 'Escape' && drawerOpen.value) drawerOpen.value = false
}

// The drawer covers the page on a phone, so it must not survive a navigation.
watch(() => router.currentRoute.value.path, () => {
  drawerOpen.value = false
})

// A drawer that scrolls the page behind it reads as a broken overlay.
watch(drawerOpen, (open) => {
  document.body.style.overflow = open ? 'hidden' : ''
})

function isActive(item) {
  const path = router.currentRoute.value.path
  return item.exact ? path === item.to : path.startsWith(item.to)
}

async function switchLocale(event) {
  await loadMessages(event.target.value)
}

function handleSignOut() {
  signOut()
  router.replace({ name: 'login' })
}

const version = computed(() => store.meta?.version || 'dev')
</script>

<template>
  <div v-if="!signedIn" class="auth-shell">
    <RouterView />
  </div>

  <div v-else class="shell" :class="{ 'rail-pinned': pinned }">
    <!-- Phone: the rail is gone and this is the only way back to the menu. -->
    <button
      class="drawer-handle"
      type="button"
      :aria-label="t('nav.menu')"
      :aria-expanded="drawerOpen"
      @click="drawerOpen = true"
    >
      <Icon name="menu" :size="18" />
    </button>

    <aside
      class="sidebar"
      :class="{ expanded }"
      @mouseenter="hovered = true"
      @mouseleave="hovered = false"
    >
      <div class="sider-brand">
        <RouterLink to="/" class="brand-block" :title="t('app.name')">
          <span class="brand-mark">W</span>
          <span v-show="expanded" class="brand-name">{{ t('app.name') }}</span>
        </RouterLink>

        <div v-show="expanded" class="brand-actions">
          <button
            class="sidebar-pin"
            type="button"
            :class="{ on: pinned }"
            :aria-pressed="pinned"
            :title="pinned ? t('nav.unpin') : t('nav.pin')"
            @click="togglePinned"
          >
            <Icon :name="pinned ? 'pinFilled' : 'pin'" :size="16" />
          </button>
        </div>
      </div>

      <nav class="sider-nav">
        <RouterLink
          v-for="item in nav"
          :key="item.to"
          :to="item.to"
          class="navlink"
          :class="{ active: isActive(item) }"
          :title="t(item.key)"
        >
          <Icon :name="item.icon" :size="16" />
          <span v-show="expanded" class="label">{{ t(item.key) }}</span>
        </RouterLink>
      </nav>

      <div class="sider-utility">
        <label class="navlink as-select" :title="t('nav.language')">
          <Icon name="translate" :size="16" />
          <span v-show="expanded" class="label">
            <select :value="store.locale" :aria-label="t('nav.language')" @change="switchLocale">
              <option v-for="l in store.meta?.locales || ['en']" :key="l" :value="l">
                {{ l === 'fa' ? 'فارسی' : 'English' }}
              </option>
            </select>
          </span>
        </label>

        <button
          class="navlink as-button"
          type="button"
          :title="`${t('auth.signOut')} — ${store.admin?.username}`"
          @click="handleSignOut"
        >
          <Icon name="logout" :size="16" />
          <span v-show="expanded" class="label">{{ t('auth.signOut') }}</span>
        </button>
      </div>

      <div class="sider-footer">
        <a
          class="sider-version"
          :href="REPO_URL"
          target="_blank"
          rel="noopener noreferrer"
          :title="`W-UI ${version}`"
        >
          <Icon name="github" :size="16" />
          <span v-show="expanded" class="sider-version-text">{{ version }}</span>
        </a>
      </div>
    </aside>

    <!-- Phone drawer. Same items, laid out for a thumb rather than a pointer. -->
    <Transition name="drawer">
      <div v-if="drawerOpen" class="drawer-scrim" @click="drawerOpen = false">
        <aside class="drawer" role="dialog" aria-modal="true" @click.stop>
          <div class="drawer-header">
            <span class="brand-block">
              <span class="brand-mark">W</span>
              <span class="brand-name">{{ t('app.name') }}</span>
            </span>
            <button class="drawer-close" type="button" :aria-label="t('common.close')" @click="drawerOpen = false">
              <Icon name="close" :size="16" />
            </button>
          </div>

          <nav class="drawer-menu">
            <RouterLink
              v-for="item in nav"
              :key="item.to"
              :to="item.to"
              class="navlink"
              :class="{ active: isActive(item) }"
            >
              <Icon :name="item.icon" :size="16" />
              <span class="label">{{ t(item.key) }}</span>
            </RouterLink>
          </nav>

          <div class="drawer-utility">
            <label class="navlink as-select">
              <Icon name="translate" :size="16" />
              <span class="label">
                <select :value="store.locale" :aria-label="t('nav.language')" @change="switchLocale">
                  <option v-for="l in store.meta?.locales || ['en']" :key="l" :value="l">
                    {{ l === 'fa' ? 'فارسی' : 'English' }}
                  </option>
                </select>
              </span>
            </label>
            <button class="navlink as-button" type="button" @click="handleSignOut">
              <Icon name="logout" :size="16" />
              <span class="label">{{ t('auth.signOut') }}</span>
            </button>
          </div>

          <div class="drawer-footer">
            <a class="sider-version" :href="REPO_URL" target="_blank" rel="noopener noreferrer">
              <Icon name="github" :size="16" />
              <span class="sider-version-text">{{ version }}</span>
            </a>
          </div>
        </aside>
      </div>
    </Transition>

    <main class="main">
      <div
        v-if="store.meta && !store.meta.enforcementActive"
        class="banner warn"
        role="status"
      >
        <Icon name="alert" :size="15" />
        <span>{{ t('enforcement.unavailable') }}</span>
      </div>

      <RouterView />
    </main>
  </div>

  <Transition name="fade">
    <div v-if="store.toast" class="toast" :class="store.toast.kind" role="status">
      {{ store.toast.message }}
    </div>
  </Transition>
</template>

<style scoped>
/* The sign-in screen paints its own full-viewport background, so the shell
   only has to stay out of its way. */
.auth-shell {
  min-height: 100%;
}

.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.2s ease;
}
.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}

.drawer-enter-active,
.drawer-leave-active {
  transition: opacity 0.2s ease;
}
.drawer-enter-active .drawer,
.drawer-leave-active .drawer {
  transition: transform 0.22s ease;
}
.drawer-enter-from,
.drawer-leave-to {
  opacity: 0;
  /* A transition only advances while the tab is visible. Open the drawer, switch
     tabs, come back, and the leave can be left half-finished — an invisible
     full-screen scrim that swallows every click on the page. Making the
     invisible states transparent to the pointer keeps that harmless. */
  pointer-events: none;
}
.drawer-enter-from .drawer,
.drawer-leave-to .drawer {
  transform: translateX(-100%);
}
[dir='rtl'] .drawer-enter-from .drawer,
[dir='rtl'] .drawer-leave-to .drawer {
  transform: translateX(100%);
}
</style>
