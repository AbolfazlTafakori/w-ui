<script setup>
import { computed, onBeforeUnmount, onErrorCaptured, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter, RouterLink, RouterView } from 'vue-router'
import { store, t, notify, signOut } from './lib/store.js'
import Icon from './components/Icon.vue'
import AntIcon from './components/AntIcon.vue'
import { themeMode, cycleTheme } from './lib/theme.js'

const router = useRouter()
const route = useRoute()

const signedIn = computed(() => !!store.admin)

// The order traffic actually flows in: what comes in, who it belongs to, then
// where it goes out and by which route. Settings and the config templates sit
// below as collapsible groups, and the docs last.
//
// It is also, item for item, the order 3x-ui uses, which is worth keeping on
// purpose: an operator moving between panels should not have to hunt for the
// page they already know the position of. What we have and it does not goes in
// beside the thing it is about, rather than appended where it would read as
// something else — Sharing reports on customers, so it sits with Customers,
// not between Routing and Settings where it looked like an admin page.
const nav = [
  { to: '/', key: 'nav.overview', icon: 'DashboardOutlined', exact: true },
  { to: '/interfaces', key: 'nav.interfaces', icon: 'ImportOutlined' },
  { to: '/clients', key: 'nav.clients', icon: 'TeamOutlined' },
  { to: '/sharing', key: 'nav.sharing', icon: 'EyeOutlined' },
  { to: '/groups', key: 'nav.groups', icon: 'TagsOutlined' },
  { to: '/nodes', key: 'nav.nodes', icon: 'ClusterOutlined' },
  { to: '/hosts', key: 'nav.hosts', icon: 'GlobalOutlined' },
  { to: '/outbounds', key: 'nav.outbounds', icon: 'ExportOutlined' },
  { to: '/routing', key: 'nav.routing', icon: 'SwapOutlined' },
  {
    key: 'nav.settings',
    icon: 'SettingOutlined',
    children: [
      // Exactly 3x-ui's five, in its order. An operator who has run one panel
      // should find these where they left them, and a menu that grows an entry
      // every time something is added stops being a place anyone can find
      // anything. The language is chosen on the General page, as it is there,
      // rather than being a line in the menu as well.
      { to: '/settings/general', key: 'settings.tab.general', icon: 'SettingOutlined' },
      { to: '/settings/security', key: 'settings.tab.security', icon: 'SafetyOutlined' },
      { to: '/settings/telegram', key: 'settings.tab.notify', icon: 'MessageOutlined' },
      { to: '/settings/email', key: 'settings.tab.email', icon: 'MailOutlined' },
      { to: '/settings/subscription', key: 'settings.tab.subscription', icon: 'CloudServerOutlined' },
    ],
  },
  {
    // 3x-ui's Xray group, for the engine this panel runs instead: the
    // basics, the balancers, DNS and the raw template, then our own
    // generated-config and log pages.
    key: 'nav.engine',
    icon: 'ToolOutlined',
    children: [
      { to: '/engine/basic', key: 'eng.basicTemplate', icon: 'SettingOutlined' },
      { to: '/engine/balancer', key: 'eng.balancers', icon: 'ClusterOutlined' },
      { to: '/engine/dns', key: 'eng.dnsMenu', icon: 'DatabaseOutlined' },
      { to: '/engine/advanced', key: 'eng.advancedTemplate', icon: 'CodeOutlined' },
      { to: '/configs/templates', key: 'nav.configs.templates', icon: 'FileTextOutlined' },
      { to: '/configs/logs', key: 'nav.configs.logs', icon: 'InfoCircleOutlined' },
    ],
  },
  { to: '/api-docs', key: 'nav.api', icon: 'ApiOutlined' },
]

// Which collapsible groups are open. A group containing the current page opens
// itself, so arriving by URL never leaves the menu pointing somewhere else.
const openGroups = ref(new Set())

function groupHasActive(item) {
  return !!item.children?.some((c) => route.path.startsWith(c.to))
}
function isGroupOpen(item) {
  return openGroups.value.has(item.key) || groupHasActive(item)
}
function toggleGroup(item) {
  const next = new Set(openGroups.value)
  if (next.has(item.key)) next.delete(item.key)
  else next.add(item.key)
  // A group holding the current page cannot be closed by clicking it -- the
  // page would stay open with its own entry hidden, which reads as the menu
  // having lost track of where you are.
  if (groupHasActive(item)) next.add(item.key)
  openGroups.value = next
}

const REPO_URL = 'https://github.com/AbolfazlTafakori/w-ui'
const PINNED_KEY = 'wui.sidebar.pinned'

// The rail is narrow until pointed at. Pinning is a deliberate choice to give
// up the space permanently, so it has to survive a reload — an operator who
// pinned it does not want to pin it again on every visit.
const pinned = ref(false)
const hovered = ref(false)
const drawerOpen = ref(false)

// The progress bar tracks work, not navigation.
//
// Tying it to the router looked right and was wrong: these routes resolve in a
// few milliseconds, so the bar either never appeared or flashed as a glitch.
// What an operator actually waits on is the request the page makes once it is
// mounted. api.js counts those and says when any has been outstanding long
// enough to be worth admitting to.
const working = ref(false)
function onBusy(e) {
  working.value = !!e.detail
}

onMounted(() => {
  window.addEventListener('wui:busy', onBusy)
  try {
    pinned.value = localStorage.getItem(PINNED_KEY) === 'true'
  } catch {
    // Private windows and blocked site data both throw here. A rail that
    // forgets its state is a much smaller problem than a panel that will not
    // render, so this is deliberately swallowed.
  }
  document.addEventListener('keydown', onKeydown)
})

onBeforeUnmount(() => {
  document.removeEventListener('keydown', onKeydown)
  window.removeEventListener('wui:busy', onBusy)
})

function togglePinned() {
  pinned.value = !pinned.value
  try {
    localStorage.setItem(PINNED_KEY, String(pinned.value))
  } catch {
    /* see above */
  }
}

const expanded = computed(() => pinned.value || hovered.value)

// On the rail a group opens beside it, as Ant's collapsed submenu popup
// does, rather than in place. Held here so hovering the title and then the
// popup is one visit.
const popup = ref(null) // { item, top }
let popupTimer = null
function showPopup(item, e) {
  if (expanded.value) return
  clearTimeout(popupTimer)
  const r = e.currentTarget.getBoundingClientRect()
  popup.value = { item, top: r.top, left: r.right + 4 }
}
function hidePopup() {
  clearTimeout(popupTimer)
  popupTimer = setTimeout(() => (popup.value = null), 100)
}
function keepPopup() {
  clearTimeout(popupTimer)
}
watch(expanded, (v) => {
  if (v) popup.value = null
})

// The inline submenu opens and closes with Ant's height motion: measured,
// then driven from 0 to that height and back.
function collapseEnter(el) {
  el.style.height = '0px'
  requestAnimationFrame(() => {
    el.style.height = el.scrollHeight + 'px'
  })
}
function collapseAfter(el) {
  el.style.height = ''
}
function collapseLeave(el) {
  el.style.height = el.scrollHeight + 'px'
  requestAnimationFrame(() => {
    el.style.height = '0px'
  })
}

const REPO_DOCS = 'https://github.com/AbolfazlTafakori/w-ui#readme'

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

function handleSignOut() {
  signOut()
  router.replace({ name: 'login' })
}

const version = computed(() => store.meta?.version || 'dev')

// A render error anywhere below here would otherwise leave a blank page: Vue
// unmounts the tree it could not render, and the operator is left with nothing
// at all - no message, no navigation, no way to tell a broken panel from a
// broken connection.
const crash = ref(null)

onErrorCaptured((err, _instance, info) => {
  // An error from the API is not a broken page: it is a request that failed,
  // and some handler let it escape. Showing "this page could not be displayed"
  // for "that port is out of range" is worse than the bug it is reporting, so
  // it is surfaced as what it is and the page is left standing.
  if (err?.name === 'ApiError') {
    notify(err.message, 'error')
    console.warn('an API error reached the error boundary; a handler did not catch it', err)
    return false
  }

  crash.value = { message: err?.message || String(err), where: info }
  // Kept in the console too: the message on screen is for the operator, the
  // stack is for whoever they send it to.
  console.error('W-UI render error', err, info)
  return false
})

// Navigating away is the most likely thing to fix a broken view, so a route
// change clears the error rather than trapping the operator on it.
watch(
  () => router.currentRoute.value.fullPath,
  () => {
    crash.value = null
  },
)

function reload() {
  window.location.reload()
}
</script>

<template>
  <div v-if="!signedIn" class="auth-shell">
    <RouterView />
  </div>

  <div v-else class="shell" :class="{ 'rail-pinned': pinned }">
    <!-- Acknowledges the click before the new page has anything to show. -->
    <div v-if="store.navigating || working" class="navbar-progress" role="presentation"></div>

    <!-- Phone: the rail is gone and this is the only way back to the menu. -->
    <button
      class="drawer-handle"
      type="button"
      :aria-label="t('nav.menu')"
      :aria-expanded="drawerOpen"
      @click="drawerOpen = true"
    >
      <AntIcon name="MenuOutlined" />
    </button>

    <aside
      class="sidebar"
      :class="{ expanded }"
      @mouseenter="hovered = true"
      @mouseleave="hovered = false"
    >
      <!-- Their brand row: the name, and with the rail open the pin, the docs
           and the theme. The name toggles the pin too, for a screen with no
           pointer to hover with. -->
      <div class="sider-brand">
        <button class="brand-block" type="button" :aria-expanded="expanded" :title="pinned ? t('nav.unpin') : t('nav.pin')" @click="togglePinned">
          <span class="brand-text">{{ expanded ? t('app.name') : 'W' }}</span>
        </button>
        <div v-if="expanded" class="brand-actions">
          <button class="sidebar-pin" type="button" :class="{ on: pinned }" :aria-pressed="pinned" :title="pinned ? t('nav.unpin') : t('nav.pin')" @click="togglePinned">
            <AntIcon :name="pinned ? 'PushpinFilled' : 'PushpinOutlined'" />
          </button>
          <a class="sidebar-docs" :href="REPO_DOCS" target="_blank" rel="noopener noreferrer" :title="t('nav.docs')" :aria-label="t('nav.docs')"><AntIcon name="ReadOutlined" /></a>
          <button class="sidebar-theme-cycle" type="button" :title="t('nav.theme')" :aria-label="t('nav.theme')" @click="cycleTheme">
            <AntIcon :name="themeMode === 'light' ? 'SunOutlined' : themeMode === 'dark' ? 'MoonOutlined' : 'MoonFilled'" />
          </button>
        </div>
      </div>

      <!-- Their inline Menu: items, and two submenus that open in place. -->
      <ul class="amenu-inline sider-nav" role="menu">
        <template v-for="item in nav" :key="item.key || item.to">
          <li v-if="!item.children" role="none">
            <RouterLink :to="item.to" class="amenu-item" :class="{ selected: isActive(item) }" role="menuitem" :title="expanded ? '' : t(item.key)">
              <AntIcon :name="item.icon" />
              <span class="amenu-title">{{ t(item.key) }}</span>
            </RouterLink>
          </li>
          <li v-else class="amenu-submenu" :class="{ open: isGroupOpen(item) && expanded, active: groupHasActive(item) }" role="none" @mouseenter="showPopup(item, $event)" @mouseleave="hidePopup">
            <button type="button" class="amenu-submenu-title" :aria-expanded="isGroupOpen(item)" :title="expanded ? '' : t(item.key)" @click="toggleGroup(item)">
              <AntIcon :name="item.icon" />
              <span class="amenu-title">{{ t(item.key) }}</span>
              <i class="amenu-submenu-arrow"></i>
            </button>
            <Transition name="amenu-collapse" @enter="collapseEnter" @after-enter="collapseAfter" @leave="collapseLeave">
              <ul v-if="isGroupOpen(item) && expanded" class="amenu-sub" role="menu">
                <li v-for="child in item.children" :key="child.to" role="none">
                  <RouterLink :to="child.to" class="amenu-item" :class="{ selected: route.path.startsWith(child.to) }" role="menuitem">
                    <AntIcon :name="child.icon" />
                    <span class="amenu-title">{{ t(child.key) }}</span>
                  </RouterLink>
                </li>
              </ul>
            </Transition>
          </li>
        </template>
      </ul>

      <ul class="amenu-inline sider-utility" role="menu">
        <li role="none">
          <button class="amenu-item" type="button" role="menuitem" :title="expanded ? '' : `${t('auth.signOut')} — ${store.admin?.username}`" @click="handleSignOut">
            <AntIcon name="LogoutOutlined" />
            <span class="amenu-title">{{ t('auth.signOut') }}</span>
          </button>
        </li>
      </ul>

      <div class="sider-footer">
        <a class="sider-version" :href="REPO_URL" target="_blank" rel="noopener noreferrer" :title="`W-UI ${version}`">
          <AntIcon name="GithubOutlined" />
          <span v-if="expanded" class="sider-version-text">{{ version }}</span>
        </a>
      </div>
    </aside>

    <!-- A group's children beside the rail while it is collapsed. -->
    <Teleport to="body">
      <ul v-if="popup && !expanded" class="amenu-popup" role="menu" :style="{ top: popup.top + 'px', left: popup.left + 'px' }" @mouseenter="keepPopup" @mouseleave="hidePopup">
        <li v-for="child in popup.item.children" :key="child.to" role="none">
          <RouterLink :to="child.to" class="amenu-item" :class="{ selected: route.path.startsWith(child.to) }" role="menuitem" @click="popup = null">
            <AntIcon :name="child.icon" />
            <span class="amenu-title">{{ t(child.key) }}</span>
          </RouterLink>
        </li>
      </ul>
    </Teleport>

    <!-- Phone drawer: their Drawer, with the same menu laid out at 48px rows. -->
    <Transition name="drawer">
      <div v-if="drawerOpen" class="drawer-scrim" @click="drawerOpen = false">
        <aside class="drawer" role="dialog" aria-modal="true" @click.stop>
          <div class="drawer-header">
            <span class="brand-block"><span class="brand-text">{{ t('app.name') }}</span></span>
            <div class="drawer-header-actions">
              <a class="sidebar-docs" :href="REPO_DOCS" target="_blank" rel="noopener noreferrer" :title="t('nav.docs')"><AntIcon name="ReadOutlined" /></a>
              <button class="sidebar-theme-cycle" type="button" :title="t('nav.theme')" @click="cycleTheme">
                <AntIcon :name="themeMode === 'light' ? 'SunOutlined' : themeMode === 'dark' ? 'MoonOutlined' : 'MoonFilled'" />
              </button>
              <button class="drawer-close" type="button" :aria-label="t('common.close')" @click="drawerOpen = false"><AntIcon name="CloseOutlined" /></button>
            </div>
          </div>

          <ul class="amenu-inline drawer-menu" role="menu">
            <template v-for="item in nav" :key="item.key || item.to">
              <li v-if="!item.children" role="none">
                <RouterLink :to="item.to" class="amenu-item" :class="{ selected: isActive(item) }" role="menuitem">
                  <AntIcon :name="item.icon" />
                  <span class="amenu-title">{{ t(item.key) }}</span>
                </RouterLink>
              </li>
              <li v-else class="amenu-submenu" :class="{ open: isGroupOpen(item), active: groupHasActive(item) }" role="none">
                <button type="button" class="amenu-submenu-title" :aria-expanded="isGroupOpen(item)" @click="toggleGroup(item)">
                  <AntIcon :name="item.icon" />
                  <span class="amenu-title">{{ t(item.key) }}</span>
                  <i class="amenu-submenu-arrow"></i>
                </button>
                <Transition name="amenu-collapse" @enter="collapseEnter" @after-enter="collapseAfter" @leave="collapseLeave">
                  <ul v-if="isGroupOpen(item)" class="amenu-sub" role="menu">
                    <li v-for="child in item.children" :key="child.to" role="none">
                      <RouterLink :to="child.to" class="amenu-item" :class="{ selected: route.path.startsWith(child.to) }" role="menuitem">
                        <AntIcon :name="child.icon" />
                        <span class="amenu-title">{{ t(child.key) }}</span>
                      </RouterLink>
                    </li>
                  </ul>
                </Transition>
              </li>
            </template>
          </ul>

          <ul class="amenu-inline drawer-utility" role="menu">
            <li role="none">
              <button class="amenu-item" type="button" role="menuitem" @click="handleSignOut">
                <AntIcon name="LogoutOutlined" />
                <span class="amenu-title">{{ t('auth.signOut') }}</span>
              </button>
            </li>
          </ul>

          <div class="drawer-footer">
            <a class="sider-version" :href="REPO_URL" target="_blank" rel="noopener noreferrer">
              <AntIcon name="GithubOutlined" />
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

      <!-- The navigation stays up, so this is a broken page rather than a
           broken panel and there is somewhere to go from here. -->
      <section v-if="crash" class="card error-state" role="alert">
        <span class="error-mark"><Icon name="alert" :size="20" /></span>
        <h2>{{ t('error.pageBroke') }}</h2>
        <p class="error-detail">{{ crash.message }}</p>
        <p class="muted small">{{ crash.where }}</p>
        <div class="error-actions">
          <button class="btn" @click="reload">
            <Icon name="refresh" :size="15" />
            <span>{{ t('error.reload') }}</span>
          </button>
          <RouterLink to="/" class="btn ghost">{{ t('nav.overview') }}</RouterLink>
        </div>
      </section>

      <!-- One page gives way to the next rather than replacing it in a frame.
           Keyed on the path so it runs on navigation and not on a query change,
           which is what a filter or a page number is: re-animating the whole
           view every time somebody types in a search box would be motion for
           its own sake.

           The key alone does this. A <Transition mode="out-in"> was tried and
           removed: it holds the new page back until the old one has finished
           leaving, and a second navigation arriving during that wait left the
           panel showing nothing at all, permanently. An animation is not worth
           a blank screen, and the key gives the same entrance without the
           coordination that can get stuck. -->
      <RouterView v-else v-slot="{ Component }">
        <component :is="Component" :key="route.path" />
      </RouterView>
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
