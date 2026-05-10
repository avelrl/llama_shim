import { defineConfig } from "vite";
import solid from "vite-plugin-solid";

export default defineConfig({
  base: "./",
  plugins: [solid()],
  build: {
    outDir: "../../internal/httpapi/operator_ui_dist",
    emptyOutDir: true,
    sourcemap: false,
    rollupOptions: {
      output: {
        entryFileNames: "assets/[name].js",
        chunkFileNames: "assets/[name].js",
        assetFileNames: "assets/[name][extname]"
      }
    }
  }
});
