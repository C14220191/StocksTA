import { useState } from "react";
import { Link, Navigate, useNavigate, useSearchParams } from "react-router-dom";
import { getAuthConfig, register } from "../../lib/api.js";
import { safeNext, saveSession } from "../../lib/auth.js";
import useApi from "../../lib/useApi.js";
import useAuth from "../../lib/useAuth.js";
import { AuthCard, Divider, Field, GoogleButton, MailLogNote, Notice, SubmitButton, linkClass } from "../../components/AuthForm.jsx";
import useForm from "../../lib/useForm.js";
import ResendVerification from "./ResendVerification.jsx";
import { AuthNotReady } from "./LoginPage.jsx";

// Daftar akun baru (role user). Backend memvalidasi semua isian dan mengembalikan kesalahan per isian;
// email juga dicek domainnya (harus ada dan menerima email), lalu dikirimi link verifikasi.
export default function RegisterPage() {
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const { user } = useAuth();
  const next = safeNext(params.get("next"));
  const { data: cfg } = useApi((signal) => getAuthConfig(signal), []);
  const form = useForm({ name: "", email: "", password: "", password_confirm: "" });
  const [done, setDone] = useState(null); // respons daftar (menunggu verifikasi email)

  if (user && !done) return <Navigate to={next} replace />;

  const submit = async (e) => {
    e.preventDefault();
    const r = await form.run(register);
    if (!r) return;
    if (r.token) {
      saveSession(r); // verifikasi email tidak diwajibkan backend
      navigate(next, { replace: true });
    } else {
      setDone(r);
    }
  };

  if (done) {
    const email = done.user.email;
    return (
      <AuthCard title="Cek email Anda" subtitle="Satu langkah lagi untuk mengaktifkan akun.">
        <p className="mt-4 text-sm leading-relaxed">
          Link verifikasi sudah dikirim ke <span className="font-semibold">{email}</span>. Buka link itu (berlaku 24 jam), lalu
          masuk. Tidak ada di inbox? Cek folder spam.
        </p>
        <MailLogNote mail={done.mail} />
        <ResendVerification email={email} />
        <Link
          to={`/login?email=${encodeURIComponent(email)}${next !== "/" ? `&next=${encodeURIComponent(next)}` : ""}`}
          className={`mt-6 block text-center text-xs ${linkClass}`}
        >
          Ke halaman masuk
        </Link>
      </AuthCard>
    );
  }

  return (
    <AuthCard
      title="Daftar"
      subtitle="Buat akun dengan email yang aktif."
      footer={
        <>
          Sudah punya akun?{" "}
          <Link to={next !== "/" ? `/login?next=${encodeURIComponent(next)}` : "/login"} className={linkClass}>
            Masuk
          </Link>
        </>
      }
    >
      <AuthNotReady cfg={cfg} />
      {form.message && <Notice tone="error">{form.message.text}</Notice>}
      <form onSubmit={submit} noValidate>
        <Field id="name" label="Nama" autoComplete="name" autoFocus {...form.bind("name")} />
        <Field
          id="email"
          label="Email"
          type="email"
          autoComplete="email"
          hint="Link verifikasi dikirim ke email ini."
          {...form.bind("email")}
        />
        <Field
          id="password"
          label="Kata sandi"
          type="password"
          autoComplete="new-password"
          hint="Minimal 8 karakter, berisi huruf dan angka."
          {...form.bind("password")}
        />
        <Field id="password_confirm" label="Ulangi kata sandi" type="password" autoComplete="new-password" {...form.bind("password_confirm")} />
        <SubmitButton busy={form.busy} busyLabel="Mendaftarkan...">
          Daftar
        </SubmitButton>
      </form>
      {cfg?.google && (
        <>
          <Divider />
          <GoogleButton next={next} label="Daftar dengan Google" />
        </>
      )}
    </AuthCard>
  );
}
