import { defineConfig } from "vitest/config";
import path from "node:path";

export default defineConfig({
  esbuild: { jsx: "automatic" },
  resolve: {
    alias: {
      "@": path.resolve(import.meta.dirname, "src"),
      "next/link": path.resolve(import.meta.dirname, "src/lib/next-link-shim.tsx"),
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test-setup.ts"],
    include: ["src/**/*.test.ts*"],
    pool: "forks",
    maxWorkers: 1,
    minWorkers: 1,
  },
});
