import { googleStartUrl } from "../lib/api.js";

// Komponen bersama halaman akun (login, daftar, lupa / reset kata sandi, verifikasi, profil).
// Validasi dilakukan backend; pesan kesalahan per isian (ApiError.fields) ditampilkan di bawah
// isian masing-masing, pesan umum di atas form.

export function AuthCard({ title, subtitle, children, footer }) {
  return (
    <div className="mx-auto w-full max-w-sm py-4">
      <div className="rounded-xl border border-line bg-panel p-6">
        <h1 className="text-lg font-bold tracking-tight">{title}</h1>
        {subtitle && <p className="mt-1 text-xs text-muted">{subtitle}</p>}
        {children}
      </div>
      {footer && <div className="mt-4 text-center text-xs text-muted">{footer}</div>}
    </div>
  );
}

export function Field({ id, label, error, hint, className = "", ...input }) {
  const describedBy = error ? `${id}-error` : hint ? `${id}-hint` : undefined;
  return (
    <div className={`mt-4 ${className}`}>
      <label htmlFor={id} className="block text-xs font-semibold text-muted">
        {label}
      </label>
      <input
        id={id}
        name={id}
        aria-invalid={error ? true : undefined}
        aria-describedby={describedBy}
        className={`mt-2 w-full rounded-lg border bg-canvas px-3 py-2 text-sm text-ink outline-none transition-colors focus:border-line-strong disabled:opacity-60 ${
          error ? "border-down" : "border-line"
        }`}
        {...input}
      />
      {error ? (
        <p id={`${id}-error`} className="mt-1.5 text-xs text-down">
          {error}
        </p>
      ) : (
        hint && (
          <p id={`${id}-hint`} className="mt-1.5 text-xs text-muted">
            {hint}
          </p>
        )
      )}
    </div>
  );
}

const TONES = {
  info: "bg-panel-2 text-muted",
  ok: "bg-panel-2 text-up",
  error: "border border-down/40 bg-panel-2 text-down",
};

export function Notice({ tone = "info", children, className = "" }) {
  if (!children) return null;
  return (
    <div role={tone === "error" ? "alert" : "status"} className={`mt-4 rounded-lg px-3 py-2 text-xs ${TONES[tone]} ${className}`}>
      {children}
    </div>
  );
}

export function SubmitButton({ busy, busyLabel = "Memproses...", children, disabled }) {
  return (
    <button
      type="submit"
      disabled={busy || disabled}
      className="mt-5 w-full rounded-lg bg-panel-2 px-4 py-2 text-sm font-semibold text-ink transition-colors hover:text-hold disabled:cursor-not-allowed disabled:opacity-40"
    >
      {busy ? busyLabel : children}
    </button>
  );
}

// Backend tanpa SMTP_HOST tidak mengirim email: link dicetak ke log backend.
export function MailLogNote({ mail }) {
  if (mail !== "log") return null;
  return (
    <Notice>
      Mode pengembangan: SMTP belum diatur di backend, jadi email tidak benar-benar dikirim. Salin link dari log backend
      (<code className="text-ink">logs/backend.err.log</code>, cari baris <code className="text-ink">EMAIL</code>).
    </Notice>
  );
}

export function Divider({ label = "atau" }) {
  return (
    <div className="mt-5 flex items-center gap-3 text-xs text-muted">
      <span className="h-px flex-1 bg-line" />
      {label}
      <span className="h-px flex-1 bg-line" />
    </div>
  );
}

// Tombol login Google: navigasi penuh ke backend (bukan fetch). Hanya tampil kalau backend
// dikonfigurasi (GOOGLE_CLIENT_ID / GOOGLE_CLIENT_SECRET).
export function GoogleButton({ next, label = "Masuk dengan Google" }) {
  return (
    <a
      href={googleStartUrl(next)}
      className="mt-5 flex w-full items-center justify-center gap-3 rounded-lg border border-line bg-canvas px-4 py-2 text-sm font-semibold text-ink transition-colors hover:border-line-strong"
    >
      <svg aria-hidden="true" viewBox="0 0 48 48" className="h-4 w-4">
        <path fill="#FFC107" d="M43.6 20.5H42V20H24v8h11.3C33.7 32.7 29.2 36 24 36c-6.6 0-12-5.4-12-12s5.4-12 12-12c3.1 0 5.8 1.2 7.9 3.1l5.7-5.7C34 6.1 29.3 4 24 4 12.9 4 4 12.9 4 24s8.9 20 20 20 20-8.9 20-20c0-1.3-.1-2.4-.4-3.5z" />
        <path fill="#FF3D00" d="m6.3 14.7 6.6 4.8C14.7 15.1 19 12 24 12c3.1 0 5.8 1.2 7.9 3.1l5.7-5.7C34 6.1 29.3 4 24 4 16.3 4 9.7 8.3 6.3 14.7z" />
        <path fill="#4CAF50" d="M24 44c5.2 0 9.9-2 13.4-5.2l-6.2-5.2C29.2 35.1 26.7 36 24 36c-5.2 0-9.6-3.3-11.3-7.9l-6.5 5C9.5 39.6 16.2 44 24 44z" />
        <path fill="#1976D2" d="M43.6 20.5H42V20H24v8h11.3c-.8 2.2-2.2 4.2-4.1 5.6l6.2 5.2C37 39.2 44 34 44 24c0-1.3-.1-2.4-.4-3.5z" />
      </svg>
      {label}
    </a>
  );
}

export const linkClass = "font-semibold text-ink underline decoration-line-strong underline-offset-4 hover:text-hold";
