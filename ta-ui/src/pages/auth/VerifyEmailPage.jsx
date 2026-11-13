import { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { verifyEmail } from "../../lib/api.js";
import { AuthCard, Notice, linkClass } from "../../components/AuthForm.jsx";
import ResendVerification from "./ResendVerification.jsx";

// Token sekali pakai: panggilan untuk token yang sama dipakai bersama, supaya effect yang dijalankan
// dua kali (React.StrictMode saat development) tidak membuat panggilan kedua gagal "sudah dipakai".
const inflight = new Map();
function verifyOnce(token) {
  if (!inflight.has(token)) inflight.set(token, verifyEmail(token));
  return inflight.get(token);
}

// Halaman dari link email /verify-email?token=...
export default function VerifyEmailPage() {
  const [params] = useSearchParams();
  const token = params.get("token") ?? "";
  const [state, setState] = useState(token ? { status: "checking" } : { status: "error", message: "Link verifikasi tidak lengkap." });

  useEffect(() => {
    if (!token) return undefined;
    let alive = true;
    verifyOnce(token)
      .then((r) => alive && setState({ status: "ok", email: r.email }))
      .catch((err) => alive && setState({ status: "error", message: err.message, code: err.code }));
    return () => {
      alive = false;
    };
  }, [token]);

  if (state.status === "checking") {
    return (
      <AuthCard title="Verifikasi email">
        <p className="mt-4 text-sm text-muted">Memverifikasi...</p>
      </AuthCard>
    );
  }
  if (state.status === "ok") {
    return (
      <AuthCard title="Email terverifikasi">
        <Notice tone="ok">
          Email <span className="font-semibold">{state.email}</span> sudah terverifikasi. Akun Anda aktif.
        </Notice>
        <Link
          to={`/login?notice=verified&email=${encodeURIComponent(state.email)}`}
          className="mt-5 block w-full rounded-lg bg-panel-2 px-4 py-2 text-center text-sm font-semibold text-ink hover:text-hold"
        >
          Masuk
        </Link>
      </AuthCard>
    );
  }
  return (
    <AuthCard
      title="Verifikasi gagal"
      footer={
        <Link to="/login" className={linkClass}>
          Ke halaman masuk
        </Link>
      }
    >
      <Notice tone="error">{state.message}</Notice>
      {state.code === "token_used" && (
        <p className="mt-4 text-xs text-muted">Kalau email Anda sudah terverifikasi sebelumnya, langsung masuk saja.</p>
      )}
      <p className="mt-4 text-xs text-muted">Minta link verifikasi baru:</p>
      <ResendVerification askEmail />
    </AuthCard>
  );
}
