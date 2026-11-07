type FragmentTarget = Pick<HTMLElement, 'id' | 'scrollIntoView'>
type FragmentHost = Pick<Window, 'requestAnimationFrame' | 'cancelAnimationFrame'> & {
  location: Pick<Location, 'hash'>
}

function matchesFragment(hash: string, id: string): boolean {
  try {
    return hash.startsWith('#') && decodeURIComponent(hash.slice(1)) === id
  } catch {
    return false
  }
}

/** The mounted React section replaces an SSR section with the same id. Wait
    until the replacement and its parent effects finish, then scroll that exact
    element. A changed fragment or an unmounted section cancels the attempt. */
export function scheduleInitialFragmentScroll(target: FragmentTarget, host: FragmentHost = window): () => void {
  if (!matchesFragment(host.location.hash, target.id)) return () => {}
  const frame = host.requestAnimationFrame(() => {
    if (matchesFragment(host.location.hash, target.id)) target.scrollIntoView({ block: 'start' })
  })
  return () => host.cancelAnimationFrame(frame)
}
