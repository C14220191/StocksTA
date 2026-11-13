import { Link, Navigate, useNavigate, useSearchParams } from "react-router-dom";
import { getAuthConfig, login } from "../../lib/api.js";
import { safeNext, saveSession } from "../../lib/auth.js";
import useApi from "../../lib/useApi.js";
import useAuth from "../../lib/useAuth.js";
import { AuthCard, Divider, Field, GoogleButton, Notice, SubmitButton, linkClass } from "../../components/AuthForm.jsx";
import useForm from "../../lib/useForm.js";
import ResendVerification from "./ResendVerification.jsx";

// ?error=... dari backend setelah login Google (google.go)
const GOOGLE_ERRORS = {
  google_cancelled: "Login Google dibatalkan.",
  google_state: "Sesi login Google tidak valid atau sudah lewat 10 menit. Silakan coba lagi.",
  google_failed: "Login Google gagal diverifikasi. Silakan coba lagi.",
  google_unverified: "Email akun Google Anda belum diverifikasi oleh Google, jadi tidak bisa dipakai.",
  google_conflict: "Email ini sudah tertaut ke akun Google lain. Masuk dengan kata sandi, atau hubungi admin.",
  google_disabled: "Login Google belum diaktifkan di server.",
  google_busy: "Server sedang sibuk. Coba lagi sebentar lagi.",
  account_disabled: "Akun ini dinonaktifkan admin.",
};

const NOTICES = {
  verified: "Email terverifikasi. Silakan masuk.",
  reset: "Kata sandi sudah diganti. Silakan masuk dengan kata sandi baru.",
};

export function AuthNotReady({ cfg }) {
  if (cfg?.ready !== false) return null;
  return <Notice tone="error">Fitur akun belum aktif di server: tabel akun belum dibuat (jalankan migrasi 011_users.sql).</Notice>;
}

export default function LoginPage() {
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const { user } = useAuth();
  const next = safeNext(params.get("next"));
  const { data: cfg } = useApi((signal) => getAuthConfig(signal), []);
  const form = useForm({ email: params.get("email") ?? "", password: "" });

  if (user) return <Navigate to={next} replace />;

  const submit = async (e) => {
    e.preventDefault();
    const r = await form.run(({ email, password }) => login(email, password));
    if (r) {
      saveSession(r);
      navigate(next, { replace: true });
    }
  };

  const googleError = GOOGLE_ERRORS[params.get("error")];
  const notice = params.get("reason") === "expired" ? "Sesi berakhir atau tidak sah. Silakan masuk lagi." : NOTICES[params.get("notice")];
  const withNext = (path) => (next !== "/" ? `${path}?next=${encodeURIComponent(next)}` : path);
  const email = form.values.email.trim();

  return (
    <AuthCard
      title="Masuk"
      subtitle="Masuk untuk melihat data pasar dan analisis AI."
      footer={
        <>
          Belum punya akun?{" "}
          <Link to={withNext("/register")} className={linkClass}>
            Daftar
          </Link>
        </>
      }
    >
      <AuthNotReady cfg={cfg} />
      {!form.message && notice && <Notice tone="ok">{notice}</Notice>}
      {!form.message && googleError && <Notice tone="error">{googleError}</Notice>}
      {form.message && <Notice tone="error">{form.message.text}</Notice>}
      {form.message?.code === "email_unverified" && <ResendVerification email={email} />}

      <form onSubmit={submit} noValidate>
        <Field id="email" label="Email" type="email" autoComplete="email" autoFocus {...form.bind("email")} />
        <Field id="password" label="Kata sandi" type="password" autoComplete="current-password" {...form.bind("password")} />
        <div className="mt-2 text-right text-xs">
          <Link to={`/forgot-password${email ? `?email=${encodeURIComponent(email)}` : ""}`} className="text-muted hover:text-ink">
            Lupa kata sandi?
          </Link>
        </div>
        <SubmitButton busy={form.busy} busyLabel="Memeriksa...">
          Masuk
        </SubmitButton>
      </form>

      {cfg?.google && (
        <>
          <Divider />
          <GoogleButton next={next} />
        </>
      )}
    </AuthCard>
  );
}
