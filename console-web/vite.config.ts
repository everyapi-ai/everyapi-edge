import path from 'node:path'

import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig, type PluginOption } from 'vite-plus'

// The control room ships as ONE file that Go embeds with `//go:embed
// web/index.html`. That constraint is not cosmetic: clients/edge is mirrored
// verbatim to the public everyapi-ai/everyapi-edge repo where suppliers run a
// plain `go build`, so the bundle is a committed artifact and every rebuild
// lands in a reviewable diff. A single inlined document keeps that diff to one
// path instead of a spray of content-hashed asset files, and the handler needs
// no static-asset route. Responses already carry `Cache-Control: no-store`
// (see internal/console/server.go), so content hashing would buy nothing.
//
// Written as a local plugin rather than pulling in vite-plugin-singlefile: the
// whole job is two string replacements against a bundle we fully control.
const escapeForRegExp = (value: string) => value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')

const inlineEverythingIntoHTML: PluginOption = {
  name: 'edge-console:single-file',
  enforce: 'post',
  apply: 'build',
  generateBundle(_options, bundle) {
    const document = bundle['index.html']
    if (!document || document.type !== 'asset') {
      this.error('edge-console:single-file expected an index.html asset in the bundle')
      return
    }

    let html = String(document.source)
    for (const [fileName, output] of Object.entries(bundle)) {
      if (fileName === 'index.html') continue
      const name = escapeForRegExp(fileName)

      // Every replacement goes through a function, never a string. A minified
      // bundle routinely contains `$&`, `$1` and friends inside regex-escape
      // helpers, and String.replace would expand those as substitution patterns
      // — silently splicing the matched <script> tag into the shipped JS.
      if (output.type === 'chunk') {
        const tag = new RegExp(`<script[^>]*src="[^"]*${name}"[^>]*></script>`)
        if (!tag.test(html)) {
          this.error(`edge-console:single-file could not find the script tag for ${fileName}`)
          return
        }
        // A literal `</script>` inside a JS string would end the inline block
        // early; splitting the sequence keeps it inert.
        const code = output.code.replaceAll('</script', '<\\/script')
        html = html.replace(tag, () => `<script type="module">${code}</script>`)
        delete bundle[fileName]
        continue
      }

      if (fileName.endsWith('.css')) {
        const tag = new RegExp(`<link[^>]*href="[^"]*${name}"[^>]*>`)
        if (!tag.test(html)) {
          this.error(`edge-console:single-file could not find the stylesheet link for ${fileName}`)
          return
        }
        const css = String(output.source)
        html = html.replace(tag, () => `<style>${css}</style>`)
        delete bundle[fileName]
        continue
      }

      this.error(
        `edge-console:single-file cannot inline ${fileName}; keep the console free of external assets`,
      )
      return
    }

    document.source = html
  },
}

export default defineConfig({
  plugins: [react(), tailwindcss(), inlineEverythingIntoHTML],
  test: {
    include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
  },
  resolve: {
    alias: { '@': path.resolve(import.meta.dirname, 'src') },
  },
  server: {
    port: 5175,
    // `bun run dev` talks to a locally running agent so the UI can be iterated
    // without rebuilding the Go binary. Point EDGE_CONSOLE_TARGET elsewhere to
    // develop against an agent on another host.
    proxy: {
      '/api': {
        target: process.env.EDGE_CONSOLE_TARGET || 'http://127.0.0.1:8421',
        changeOrigin: true,
      },
    },
  },
  build: {
    // Emitted straight into the Go embed path. internal/console/web is not a
    // separate staging directory — it IS the compiled-in asset.
    outDir: path.resolve(import.meta.dirname, '../internal/console/web'),
    emptyOutDir: false,
    cssCodeSplit: false,
    modulePreload: false,
    // Everything ends up inside index.html, so a source map would either leak
    // as a second file or double the committed artifact. Neither is wanted for
    // an asset that ships to a public mirror.
    sourcemap: false,
    rolldownOptions: {
      checks: {
        pluginTimings: false,
      },
    },
  },
})
