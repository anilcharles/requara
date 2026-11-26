import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The Go backend runs on :8080. Proxying keeps the session cookie on the
// app's own origin and makes /auth/callback (Keycloak's redirect) reach Go.
const backend = "http://localhost:8080";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    strictPort: true,
    // Anchored patterns: a plain "/api" prefix would also swallow the app's own /apis page.
    proxy: {
      "^/api(/|$)": { target: backend },
      "^/auth(/|$)": { target: backend },
      "^/mock(/|$)": { target: backend },
    },
  },
});
