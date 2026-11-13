import { useEffect, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { exchangeGoogleCode } from "../../lib/api.js";
import { safeNext, saveSession } from "../../lib/auth.js";
import { AuthCard, Notice, linkClass } from "../../components/AuthForm.jsx";

// Kode login sekali pakai: dipakai bersama kalau effect berjalan dua kali (React.StrictMode).
const inflight = new Map();
function exchangeOnce(code) {
  if (!inflight.has(code)) inflight.set(code, exchangeGoogleCode(code));
  return inflight.get(code);
}

// /auth/google?code=... : tujuan redirect backend setelah login Google berhasil diverifikasi.
// Kode ditukar dengan token sesi lewat POST, jadi token sesi tidak pernah ada di URL.
export default function GoogleCallbackPage() {
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const code = params.get("code") ?? "";
  const [error, setError] = useState(code ? null : "Kode login Google tidak ada di URL.");

  useEffect(() => {
    if (!code) return undefined;
    let alive = true;
    exchangeOnce(code)
      .then((r) => {
        if (!alive) return;
        saveSession(r);
        navigate(safeNext(r.next), { replace: true });
      })
      .catch((err) => alive && setError(err.message));
    return () => {
      alive = false;
    };
  }, [code, navigate]);

  return (
    <AuthCard title={error ? "Login Google gagal" : "Masuk dengan Google"}>
      {error ? (
        <>
          <Notice tone="error">{error}</Notice>
          <Link to="/login" className={`mt-6 block text-center text-xs ${linkClass}`}>
            Kembali ke halaman masuk
          </Link>
        </>
      ) : (
        <p className="mt-4 text-sm text-muted">Menyelesaikan login...</p>
      )}
    </AuthCard>
  );
}
