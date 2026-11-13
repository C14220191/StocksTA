import { useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { forgotPassword } from "../../lib/api.js";
import { AuthCard, Field, MailLogNote, Notice, SubmitButton, linkClass } from "../../components/AuthForm.jsx";
import useForm from "../../lib/useForm.js";

// Minta link reset kata sandi. Jawaban backend sama untuk email terdaftar maupun tidak (supaya halaman
// ini tidak bisa dipakai untuk menebak email siapa yang punya akun).
export default function ForgotPasswordPage() {
  const [params] = useSearchParams();
  const form = useForm({ email: params.get("email") ?? "" });
  const [sent, setSent] = useState(null);

  const submit = async (e) => {
    e.preventDefault();
    const r = await form.run(({ email }) => forgotPassword(email));
    if (r) setSent(r);
  };

  return (
    <AuthCard
      title="Lupa kata sandi"
      subtitle="Masukkan email akun Anda. Kami kirim link untuk membuat kata sandi baru (berlaku 1 jam)."
      footer={
        <Link to="/login" className={linkClass}>
          Kembali ke halaman masuk
        </Link>
      }
    >
      {form.message && <Notice tone="error">{form.message.text}</Notice>}
      {sent ? (
        <>
          <Notice tone="ok">{sent.message}</Notice>
          <MailLogNote mail={sent.mail} />
        </>
      ) : (
        <form onSubmit={submit} noValidate>
          <Field id="email" label="Email" type="email" autoComplete="email" autoFocus {...form.bind("email")} />
          <SubmitButton busy={form.busy} busyLabel="Mengirim...">
            Kirim link reset
          </SubmitButton>
        </form>
      )}
    </AuthCard>
  );
}
