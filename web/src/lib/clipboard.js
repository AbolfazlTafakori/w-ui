// Putting text on the clipboard, wherever the panel is served.
//
// navigator.clipboard exists only in a secure context: HTTPS, or localhost.
// A panel reached as http://<server-ip>:<port>/ -- what most installs are,
// and every test server -- has none, and each copy button failed with "copy
// it manually". There the old way still works: select the text in a hidden
// field and ask the browser to copy it, inside the click that asked for it.
// The customer's subscription page does the same (internal/api/subpage.go).

// copyText puts text on the clipboard, or throws when the browser refuses.
// Call it straight from a click handler: the fallback only works inside the
// user's own gesture, so nothing awaited may come before it.
export async function copyText(text) {
  const value = String(text ?? '')
  if (navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(value)
      return
    } catch {
      // Refused (permissions, an unfocused document): the old way may still work.
    }
  }
  if (!legacyCopy(value)) throw new Error('copy refused')
}

// legacyCopy copies through a hidden, selected textarea. It keeps whatever
// was focused and selected before, so a copy from inside a dialog does not
// move the caret or close anything.
function legacyCopy(value) {
  const active = document.activeElement
  const selection = document.getSelection()
  const ranges = []
  if (selection) for (let i = 0; i < selection.rangeCount; i++) ranges.push(selection.getRangeAt(i))

  const ta = document.createElement('textarea')
  ta.value = value
  ta.setAttribute('readonly', '')
  ta.setAttribute('aria-hidden', 'true')
  // On screen but invisible: iOS will not select text in a field placed off it.
  Object.assign(ta.style, { position: 'fixed', top: '0', left: '0', width: '1px', height: '1px', padding: '0', border: '0', opacity: '0', fontSize: '16px' })
  // Inside an open modal dialog, so a focus trap there does not pull focus back.
  const host = (active && active.closest && active.closest('[role="dialog"]')) || document.body
  host.appendChild(ta)
  ta.focus({ preventScroll: true })
  ta.select()
  ta.setSelectionRange(0, value.length)
  let ok = false
  try {
    ok = document.execCommand('copy')
  } catch {
    ok = false
  }
  host.removeChild(ta)
  if (selection) {
    selection.removeAllRanges()
    for (const r of ranges) selection.addRange(r)
  }
  if (active && typeof active.focus === 'function') active.focus({ preventScroll: true })
  return ok
}

// canCopyImage reports whether a picture can go on the clipboard, which no
// fallback can do: the button that does it is offered only where it works.
export function canCopyImage() {
  return !!(window.isSecureContext && navigator.clipboard && navigator.clipboard.write && window.ClipboardItem)
}
