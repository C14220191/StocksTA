import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// URL backend dibaca dari .env (VITE_API_URL) di src/lib/api.js.
// Browser memanggil backend langsung, jadi backend harus mengizinkan origin Vite lewat CORS_ORIGINS.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: { port: 5173 },
});
