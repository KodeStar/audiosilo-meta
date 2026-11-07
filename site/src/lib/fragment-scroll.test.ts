import { describe, expect, it, vi } from 'vitest'
import { scheduleInitialFragmentScroll } from './fragment-scroll'

function browser(hash: string) {
  let nextFrame = 0
  const pending = new Map<number, FrameRequestCallback>()
  const host = {
    location: { hash },
    requestAnimationFrame: vi.fn((callback: FrameRequestCallback) => {
      pending.set(++nextFrame, callback)
      return nextFrame
    }),
    cancelAnimationFrame: vi.fn((frame: number) => { pending.delete(frame) }),
  }
  return {
    host,
    frame: () => {
      const callbacks = [...pending.values()]
      pending.clear()
      callbacks.forEach((callback) => callback(0))
    },
  }
}

describe('incoming section fragments after hydration', () => {
  it.each(['listening-guide', 'recording-credits'])('scrolls the mounted %s section once after the SSR replacement completes', (id) => {
    const { host, frame } = browser(`#${id}`)
    let replaced = false
    const target = { id, scrollIntoView: vi.fn(() => { expect(replaced).toBe(true) }) }
    scheduleInitialFragmentScroll(target, host)
    expect(target.scrollIntoView).not.toHaveBeenCalled()
    // The parent removes its SSR fact sheet after the section's mount effect.
    replaced = true
    frame()
    expect(target.scrollIntoView).toHaveBeenCalledExactlyOnceWith({ block: 'start' })
    frame()
    expect(target.scrollIntoView).toHaveBeenCalledTimes(1)
  })

  it('does not take over scrolling when the incoming fragment names another section or is malformed', () => {
    for (const hash of ['', '#other', '#%invalid']) {
      const { host, frame } = browser(hash)
      const target = { id: 'listening-guide', scrollIntoView: vi.fn() }
      scheduleInitialFragmentScroll(target, host)
      frame()
      expect(host.requestAnimationFrame).not.toHaveBeenCalled()
      expect(target.scrollIntoView).not.toHaveBeenCalled()
    }
  })

  it('respects navigation before the deferred scroll and cancels on unmount', () => {
    const { host, frame } = browser('#recording-credits')
    const target = { id: 'recording-credits', scrollIntoView: vi.fn() }
    scheduleInitialFragmentScroll(target, host)
    host.location.hash = '#other'
    frame()
    expect(target.scrollIntoView).not.toHaveBeenCalled()
    host.location.hash = '#recording-credits'
    const cancel = scheduleInitialFragmentScroll(target, host)
    cancel()
    frame()
    expect(target.scrollIntoView).not.toHaveBeenCalled()
  })
})
