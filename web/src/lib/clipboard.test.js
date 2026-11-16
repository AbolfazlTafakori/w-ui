import { afterEach, describe, expect, it, vi } from 'vitest'
import { copyText } from './clipboard.js'

const secure = Object.getOwnPropertyDescriptor(window, 'isSecureContext')
afterEach(() => {
  vi.restoreAllMocks()
  if (secure) Object.defineProperty(window, 'isSecureContext', secure)
  delete navigator.clipboard
  delete document.execCommand
})

function setSecure(v) {
  Object.defineProperty(window, 'isSecureContext', { value: v, configurable: true })
}

describe('copyText', () => {
  it('uses the clipboard API where the page is secure', async () => {
    setSecure(true)
    const writeText = vi.fn().mockResolvedValue()
    navigator.clipboard = { writeText }
    document.execCommand = vi.fn()
    await copyText('https://h/sub/x')
    expect(writeText).toHaveBeenCalledWith('https://h/sub/x')
    expect(document.execCommand).not.toHaveBeenCalled()
  })

  it('copies through a selected field on a plain-http panel', async () => {
    setSecure(false)
    let copied = null
    document.execCommand = vi.fn(() => {
      copied = document.activeElement.value
      return true
    })
    await copyText('http://91.0.0.1:2096/subscribe/abc')
    expect(copied).toBe('http://91.0.0.1:2096/subscribe/abc')
    expect(document.querySelectorAll('textarea').length).toBe(0)
  })

  it('falls back when the clipboard API refuses', async () => {
    setSecure(true)
    navigator.clipboard = { writeText: vi.fn().mockRejectedValue(new Error('denied')) }
    document.execCommand = vi.fn(() => true)
    await copyText('x')
    expect(document.execCommand).toHaveBeenCalledWith('copy')
  })

  it('says so when the browser refuses both ways', async () => {
    setSecure(false)
    document.execCommand = vi.fn(() => false)
    await expect(copyText('x')).rejects.toThrow()
  })

  it('gives focus back to what had it', async () => {
    setSecure(false)
    const input = document.createElement('input')
    document.body.appendChild(input)
    input.focus()
    document.execCommand = vi.fn(() => true)
    await copyText('x')
    expect(document.activeElement).toBe(input)
    input.remove()
  })
})
