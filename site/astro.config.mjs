// @ts-check
import { defineConfig } from 'astro/config'
import react from '@astrojs/react'
import sitemap from '@astrojs/sitemap'
import tailwindcss from '@tailwindcss/vite'

// https://astro.build/config
export default defineConfig({
  site: 'https://meta.audiosilo.app',
  output: 'static',
  integrations: [react(), sitemap()],
  vite: {
    plugins: [tailwindcss()],
    // /docs/api renders internal/serve/openapi.json, which sits one level above
    // this package: the spec is authored in the Go tree and served verbatim by
    // metaserve, so the page reads THAT file rather than a copy that could drift.
    // The production build resolves it either way; this is what additionally
    // lets `astro dev` read it.
    server: { fs: { allow: ['..'] } },
  },
})
