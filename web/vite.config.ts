import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import pkg from "./package.json" with { type: "json" };

// The build emits a stable, unhashed module so any host page can embed it:
//   <script type="module" src="https://<host>/oversight-element.js"></script>
//   <oversight-app api-base="https://<api>"></oversight-app>
export default defineConfig({
  plugins: [react()],
  define: { __OVERSIGHT_VERSION__: JSON.stringify(pkg.version) },
  envDir: "..",
  server: { port: 5180, strictPort: true },
  build: {
    rollupOptions: {
      output: {
        entryFileNames: "oversight-element.js",
        chunkFileNames: "chunks/[name]-[hash].js",
        assetFileNames: "assets/[name][extname]",
      },
    },
  },
});
