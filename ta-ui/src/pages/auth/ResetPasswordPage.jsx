import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { checkResetToken, resetPassword } from "../../lib/api.js";
import useApi from "../../lib/useApi.js";
import { AuthCard, Field, Notice, SubmitButton, linkClass } from "../../components/AuthForm.jsx";
import useForm from "../../lib/useForm.js";

const TOKEN_CODES = new Set(["token_invalid", "token_used", "token_expired"]);

// Halaman dari link email /reset-password?token=... Token dicek dulu (tanpa dipakai), supaya link
// kedaluwarsa langsung diberi tahu sebelum pengguna mengetik kata sandi baru.
export default function ResetPasswordPage() {
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const token = params.get("token") ?? "";
  const check = useApi(() => (token ? checkResetToken(token) : Promise.resolve(null)), [token]);
  const form = useForm({ password: "", password_confirm: "" });

  const submit = async (e) => {
    e.preventDefault();
    const r = await form.run((v) => resetPassword({ token, ...v }));
    if (r) navigate(`/login?notice=reset&email=${encodeURIComponent(r.email)}`, { replace: true });
  };

  const tokenError = !token
    ? "Link reset tidak lengkap. Buka link dari email secara utuh, atau minta link baru."
    : check.error?.message ?? (TOKEN_CODES.has(form.message?.code) ? form.message.text : null);

  if (tokenError) {
    return (
      <AuthCard title="Link tidak berlaku">
        <Notice tone="error">{tokenError}</Notice>
        <Link to="/forgot-password" className={`mt-6 block text-center text-xs ${linkClass}`}>
          Minta link reset baru
        </Link>
      </AuthCard>
    );
  }
  if (check.loading) {
    return (
      <AuthCard title="Atur ulang kata sandi">
        <p className="mt-4 text-sm text-muted">Memeriksa link...</p>
      </AuthCard>
    );
  }

  return (
    <AuthCard title="Atur ulang kata sandi" subtitle={check.data?.email ? `Untuk akun ${check.data.email}.` : undefined}>
      {form.message && <Notice tone="error">{form.message.text}</Notice>}
      <form onSubmit={submit} noValidate>
        <Field
          id="password"
          label="Kata sandi baru"
          type="password"
          autoComplete="new-password"
          autoFocus
          hint="Minimal 8 karakter, berisi huruf dan angka. Semua sesi login lama akan dikeluarkan."
          {...form.bind("password")}
        />
        <Field id="password_confirm" label="Ulangi kata sandi baru" type="password" autoComplete="new-password" {...form.bind("password_confirm")} />
        <SubmitButton busy={form.busy} busyLabel="Menyimpan...">
          Simpan kata sandi
        </SubmitButton>
      </form>
    </AuthCard>
  );
}
