import { useState } from "react";
import { Link, Navigate, useNavigate, useSearchParams } from "react-router-dom";
import { adminLogin } from "../lib/api.js";
import { getAdminToken, saveAdminSession } from "../lib/adminAuth.js";

// Tujuan setelah login: hanya halaman admin (bukan URL luar dan bukan halaman login itu sendiri).
function safeNext(next) {
  if (!next || !next.startsWith("/admin") || next.startsWith("/admin/login")) return "/admin";
  return next;
}

export default function LoginPage() {
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const next = safeNext(params.get("next"));
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(null);

  if (getAdminToken()) return <Navigate to={next} replace />;

  const submit = async (e) => {
    e.preventDefault();
    if (!password || busy) return;
    setBusy(true);
    setError(null);
    try {
      const r = await adminLogin(password);
      saveAdminSession(r.token, r.expires_at);
      navigate(next, { replace: true });
    } catch (err) {
      setError(err);
      setBusy(false);
    }
  };

  return (
    <div className="flex min-h-screen items-center justify-center px-4 font-sans">
      <form onSubmit={submit} className="w-full max-w-sm rounded-xl border border-line bg-panel p-6">
        <h1 className="text-lg font-bold tracking-tight">Admin</h1>
        <p className="mt-1 text-xs text-muted">Cached AI Inference · masuk untuk membuka halaman pemantauan.</p>
        {params.get("reason") === "expired" && !error && (
          <p role="status" className="mt-4 rounded-lg bg-panel-2 px-3 py-2 text-xs text-muted">
            Sesi berakhir atau tidak sah. Silakan masuk lagi.
          </p>
        )}
        <label className="mt-5 block text-xs font-semibold text-muted" htmlFor="admin-password">
          Kata sandi admin
        </label>
        <input
          id="admin-password"
          type="password"
          autoFocus
          autoComplete="current-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          className="mt-2 w-full rounded-lg border border-line bg-canvas px-3 py-2 text-sm text-ink outline-none focus:border-line-strong"
        />
        {error && (
          <p role="alert" className="mt-3 text-xs text-down">
            {error.message}
          </p>
        )}
        <button
          type="submit"
          disabled={!password || busy}
          className="mt-5 w-full rounded-lg bg-panel-2 px-4 py-2 text-sm font-semibold text-ink transition-colors hover:text-hold disabled:cursor-not-allowed disabled:opacity-40"
        >
          {busy ? "Memeriksa..." : "Masuk"}
        </button>
        <Link to="/" className="mt-5 block text-center text-xs text-muted hover:text-ink">
          ← Ke aplikasi pengguna
        </Link>
      </form>
    </div>
  );
}
