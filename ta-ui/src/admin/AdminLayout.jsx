import { useEffect, useState } from "react";
import { Link, Navigate, NavLink, Outlet, useLocation } from "react-router-dom";
import { getMe, signOut } from "../lib/api.js";
import { getToken, updateSessionUser } from "../lib/auth.js";
import useAuth from "../lib/useAuth.js";
import { fmtDateTime } from "./ui.jsx";

const NAV = [
  { to: "/admin", label: "Dashboard", end: true },
  { to: "/admin/prompt", label: "Prompt" },
  { to: "/admin/cache", label: "Cache & Job" },
  { to: "/admin/instruments", label: "Instrumen" },
  { to: "/admin/evaluation", label: "Evaluasi" },
  { to: "/admin/health", label: "API Health" },
  { to: "/admin/users", label: "Pengguna" },
];

// Sebelum menampilkan halaman admin, profil dicek ulang ke backend (peran bisa sudah diubah, sesi
// bisa sudah dicabut). 401 menghapus sesi (lib/api.js) sehingga user = null dan diarahkan ke login.
function useFreshProfile() {
  const [checked, setChecked] = useState(() => !getToken());
  useEffect(() => {
    if (!getToken()) return undefined;
    const ctrl = new AbortController();
    getMe(ctrl.signal)
      .then((r) => {
        updateSessionUser(r.user);
        setChecked(true);
      })
      .catch((e) => {
        if (e.name !== "AbortError") setChecked(true); // backend tidak terjangkau: halaman menampilkan errornya
      });
    return () => ctrl.abort();
  }, []);
  return checked;
}

function Forbidden({ user }) {
  return (
    <div className="flex min-h-screen items-center justify-center px-4 font-sans">
      <div className="w-full max-w-sm rounded-xl border border-line bg-panel p-6">
        <h1 className="text-lg font-bold tracking-tight">Khusus admin</h1>
        <p className="mt-2 text-sm text-muted">
          Akun <span className="text-ink">{user.email}</span> berperan user. Halaman admin hanya untuk akun berperan admin.
        </p>
        <div className="mt-5 flex gap-3">
          <Link to="/" className="rounded-lg bg-panel-2 px-4 py-2 text-xs font-semibold hover:text-hold">
            Ke beranda
          </Link>
          <button onClick={signOut} className="rounded-lg border border-line px-4 py-2 text-xs font-semibold text-muted hover:text-ink">
            Masuk dengan akun lain
          </button>
        </div>
      </div>
    </div>
  );
}

// Layout admin: sidebar di layar lebar, menu horizontal di layar sempit. Semua halaman admin hanya
// tampil untuk akun ber-role admin.
export default function AdminLayout() {
  const location = useLocation();
  const { user, expiresAt, reason } = useAuth();
  const checked = useFreshProfile();

  if (!user) {
    const next = encodeURIComponent(location.pathname + location.search);
    return <Navigate to={`/login?next=${next}${reason === "expired" ? "&reason=expired" : ""}`} replace />;
  }
  if (!checked) {
    return <p className="px-6 py-10 font-sans text-sm text-muted">Memeriksa sesi...</p>;
  }
  if (user.role !== "admin") return <Forbidden user={user} />;

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
          <div className="min-w-0 md:mt-4">
            <Link to="/account" className="block truncate text-xs font-semibold text-ink hover:text-hold" title={user.email}>
              {user.name}
            </Link>
            <p className="text-xs text-muted">Sesi sampai {fmtDateTime(expiresAt)}</p>
          </div>
          <button
            onClick={signOut}
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
