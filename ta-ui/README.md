# ta-ui

UI StocksTA (React + Vite + Tailwind): halaman pengguna (Home, Detail, Search) dan halaman admin
(`/admin`, wajib login dengan `ADMIN_PASSWORD` backend).

```bash
npm install
npm run dev          # backend lokal, VITE_API_URL di .env (default http://127.0.0.1:8080)
npm run dev:server   # backend di server lewat tunnel SSH, lihat .env.server
npm run build
```

Endpoint dan kontrak data: [docs/API-UI.md](../docs/API-UI.md).
