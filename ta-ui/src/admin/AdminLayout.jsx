import { useEffect, useState } from "react";
import { Link, Navigate, NavLink, Outlet, useLocation } from "react-router-dom";
import { getAdminSession } from "../lib/api.js";
import { ADMIN_SESSION_EVENT, clearAdminSession, getAdminExpiry, getAdminToken } from "../lib/adminAuth.js";
import { fmtDateTime } from "./ui.jsx";

const NAV = [
  { to: "/admin", label: "Dashboard", end: true },
  { to: "/admin/prompt", label: "Prompt" },
  { to: "/admin/cache", label: "Cache & Job" },
  { to: "/admin/instruments", label: "Instrumen" },
  { to: "/admin/evaluation", label: "Evaluasi" },
  { to: "/admin/health", label: "API Health" },
];

// Status sesi: checking (token ada, sedang dicek ke backend) | in | out. Token yang ditolak backend
// (401: kedaluwarsa, kata sandi diganti) dihapus oleh lib/api.js dan memicu ADMIN_SESSION_EVENT.
function useAdminSession() {
  const [state, setState] = useState(() => ({ status: getAdminToken() ? "checking" : "out", reason: null }));

  useEffect(() => {
    if (!getAdminToken()) return;
    const ctrl = new AbortController();
    getAdminSession(ctrl.signal)
      .then(() => setState({ status: "in", reason: null }))
      .catch((e) => {
        if (e.name === "AbortError") return;
        // 401/503 = tidak boleh masuk; backend tidak terjangkau = tampilkan halaman, error muncul di sana
        setState(e.status === 401 || e.status === 503 ? { status: "out", reason: "expired" } : { status: "in", reason: null });
      });
    return () => ctrl.abort();
  }, []);

  useEffect(() => {
    const onChange = (e) => {
      if (e.detail === "login") setState({ status: "in", reason: null });
      else setState({ status: "out", reason: e.detail });
    };
    window.addEventListener(ADMIN_SESSION_EVENT, onChange);
    return () => window.removeEventListener(ADMIN_SESSION_EVENT, onChange);
  }, []);

  return state;
}

// Layout admin: sidebar di layar lebar, menu horizontal di layar sempit. Semua halaman admin
// hanya tampil setelah login (LoginPage).
export default function AdminLayout() {
  const location = useLocation();
  const session = useAdminSession();

  if (session.status === "out") {
    const next = encodeURIComponent(location.pathname + location.search);
    const reason = session.reason === "expired" ? "&reason=expired" : "";
    return <Navigate to={`/admin/login?next=${next}${reason}`} replace />;
  }
  if (session.status === "checking") {
    return <p className="px-6 py-10 font-sans text-sm text-muted">Memeriksa sesi admin...</p>;
  }

  return (
    <div className="min-h-screen font-sans md:flex">
      <aside className="border-b border-line bg-canvas md:sticky md:top-0 md:h-screen md:w-56 md:shrink-0 md:border-b-0 md:border-r">
        <div className="px-5 py-5">
          <div className="text-lg font-bold tracking-tight">Admin</div>
          <div className="text-xs text-muted">Cached AI Inference</div>
        </div>
        <nav className="flex gap-1 overflow-x-auto px-3 pb-3 md:flex-col md:overflow-visible">
          {NAV.map((n) => (
            <NavLink
              key={n.to}
              to={n.to}
              end={n.end}
              className={({ isActive }) =>
                `whitespace-nowrap rounded-lg px-3 py-2 text-sm font-semibold transition-colors ${
                  isActive ? "bg-panel-2 text-ink" : "text-muted hover:text-ink"
                }`
              }
            >
              {n.label}
            </NavLink>
          ))}
        </nav>
        <div className="flex items-center justify-between gap-3 px-5 pb-4 md:block md:pb-0 md:pt-6">
          <Link to="/" className="hidden text-xs text-muted hover:text-ink md:block">
            ← Ke aplikasi pengguna
          </Link>
          <p className="text-xs text-muted md:mt-4">Sesi sampai {fmtDateTime(getAdminExpiry())}</p>
          <button
            onClick={() => clearAdminSession("logout")}
            className="rounded-lg border border-line px-3 py-1.5 text-xs font-semibold text-muted transition-colors hover:text-ink md:mt-2"
          >
            Keluar
          </button>
        </div>
      </aside>
      <main className="min-w-0 flex-1 px-4 py-8 md:px-10">
        <div className="mx-auto max-w-7xl">
          <Outlet />
        </div>
      </main>
    </div>
  );
}
