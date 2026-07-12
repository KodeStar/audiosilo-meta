// Trigger a browser download of generated text (DOM-only, no fetch). Shared by
// the import tool's bulk new-books export and the sidecar builder's
// characters/recaps download, so the object-URL dance lives in one place.

/** Download `text` as `filename` via a temporary object-URL anchor. */
export function downloadJson(text: string, filename: string): void {
  const blob = new Blob([text], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  // Defer the revoke: some browsers (Firefox/Safari) cancel the save if the
  // blob URL is freed before the download has started reading it.
  setTimeout(() => URL.revokeObjectURL(url), 0)
}
