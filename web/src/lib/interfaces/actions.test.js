import { describe, expect, it, vi } from 'vitest'
import { ref } from 'vue'
import { useInterfaceActions } from './actions.js'

// Delete on a tunnel with customers opens the one-step removal; on an
// empty one it only asks to be sure.
describe('deleting a tunnel', () => {
  const setup = () => {
    const open = { remove: vi.fn(), text: vi.fn(), importer: vi.fn(), clone: vi.fn(), attach: vi.fn(), move: vi.fn(), group: vi.fn() }
    const a = useInterfaceActions({ interfaces: ref([]), selected: ref(new Set()), busy: ref(false), load: vi.fn(), open })
    return { a, open }
  }
  it('with customers opens the removal that moves or removes them', () => {
    const { a, open } = setup()
    a.pickRow({ id: 7, name: 'wg0', clients: 3 }, 'delete')
    expect(open.remove).toHaveBeenCalledWith(expect.objectContaining({ id: 7 }))
    expect(a.ask.value).toBeNull()
  })
  it('empty only asks to be sure', () => {
    const { a, open } = setup()
    a.pickRow({ id: 8, name: 'wg1', clients: 0 }, 'delete')
    expect(open.remove).not.toHaveBeenCalled()
    expect(a.ask.value).not.toBeNull()
  })
})
