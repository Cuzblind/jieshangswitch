import path from "node:path";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Adapted from CC Switch v4.0.20: one renderer, no tray/provider entrypoints.
export default defineConfig({
  plugins: [react()],
  base: "./",
  server: { port: 1420, strictPort: true, host: "127.0.0.1" },
  resolve: { alias: { "@": path.resolve(import.meta.dirname, "src") } },
  clearScreen: false,
});
