import { useState } from "react";
import { resendVerification } from "../../lib/api.js";
import { Field, MailLogNote, Notice } from "../../components/AuthForm.jsx";

// Kirim ulang link verifikasi. askEmail: tampilkan isian email (mis. di halaman link kedaluwarsa).
export default function ResendVerification({ email = "", askEmail = false }) {
  const [value, setValue] = useState(email);
  const [state, setState] = useState({ status: "idle" }); // idle | busy | sent | error

  const send = async (e) => {
    e?.preventDefault();
    if (state.status === "busy") return;
    setState({ status: "busy" });
    try {
      const r = await resendVerification(askEmail ? value : email);
      setState({ status: "sent", message: r.message, mail: r.mail });
    } catch (err) {
      setState({ status: "error", message: err.fields?.email || err.message });
    }
  };

  const button = (
    <button
      type={askEmail ? "submit" : "button"}
      onClick={askEmail ? undefined : send}
      disabled={state.status === "busy"}
      className="mt-3 rounded-lg border border-line px-3 py-1.5 text-xs font-semibold text-ink transition-colors hover:border-line-strong disabled:opacity-40"
    >
      {state.status === "busy" ? "Mengirim..." : "Kirim ulang link verifikasi"}
    </button>
  );

  return (
    <div className="mt-4">
      {askEmail ? (
        <form onSubmit={send} noValidate>
          <Field id="resend-email" label="Email akun" type="email" autoComplete="email" value={value} onChange={(e) => setValue(e.target.value)} />
          {button}
        </form>
      ) : (
        button
      )}
      {state.status === "sent" && (
        <>
          <Notice tone="ok">{state.message}</Notice>
          <MailLogNote mail={state.mail} />
        </>
      )}
      {state.status === "error" && <Notice tone="error">{state.message}</Notice>}
    </div>
  );
}
