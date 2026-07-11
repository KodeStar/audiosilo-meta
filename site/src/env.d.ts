/// <reference path="../.astro/types.d.ts" />
/// <reference types="astro/client" />

interface ImportMetaEnv {
  /** Base URL for the metadata API. Empty string = same-origin (production
      default, since the Go server serves this built site). */
  readonly PUBLIC_API_BASE?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
