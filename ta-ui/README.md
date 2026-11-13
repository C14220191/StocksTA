# ta-ui

UI StocksTA (React + Vite + Tailwind): halaman pengguna (Home, Detail, Search; wajib login), akun
(`/login`, `/register`, `/forgot-password`, `/account`, login Google), dan halaman admin (`/admin`, wajib
akun ber-role admin). Akun demo: `admin@example.com` + `ADMIN_PASSWORD`, `user@example.com` +
`DEMO_USER_PASSWORD` (keduanya di `.env` backend). Produksi: https://stocks-ta.vercel.app, env Vercel
`VITE_API_URL` = alamat Tailscale Funnel backend (lihat docs/API-UI.md, Deployment).

```bash
npm install
npm run dev          # backend lokal, VITE_API_URL di .env (default http://127.0.0.1:8080)
npm run dev:server   # backend di server lewat tunnel SSH, lihat .env.server
npm run build
```

Endpoint dan kontrak data: [docs/API-UI.md](../docs/API-UI.md).
