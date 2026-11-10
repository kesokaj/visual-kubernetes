import tailwindcss from '@tailwindcss/vite'
import { tanstackStart } from '@tanstack/react-start/plugin/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// The Go binary embeds the client build (cmd/visual-kubernetes/embed.go);
// TestEmbedDirMatchesViteOutDir fails when the two drift apart. The server bundle only
// exists to prerender the SPA shell, so it stays in web/dist where node_modules resolve.
export const clientOutDir = '../cmd/visual-kubernetes/ui/dist'

export default defineConfig({
  environments: {
    client: { build: { outDir: clientOutDir, emptyOutDir: true } },
  },
  plugins: [
    tailwindcss(),
    // SPA mode: no server rendering; the shell is prerendered to index.html and the
    // Go binary serves it for every non-API path.
    tanstackStart({
      spa: { enabled: true, prerender: { outputPath: '/index.html' } },
    }),
    react(),
  ],
})
