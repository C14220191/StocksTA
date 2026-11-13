import { useEffect, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { changePassword, getMe, logoutAll, signOut, updateMe } from "../../lib/api.js";
import { clearSession, updateSessionUser } from "../../lib/auth.js";
import useAuth from "../../lib/useAuth.js";
import { Field, Notice, linkClass } from "../../components/AuthForm.jsx";
import useForm from "../../lib/useForm.js";

const fmtTime = (iso) =>
  iso ? new Date(iso).toLocaleString("id-ID", { day: "2-digit", month: "short", year: "numeric", hour: "2-digit", minute: "2-digit" }) : "-";

function Section({ title, children }) {
  return (
    <section className="mt-6 rounded-xl border border-line bg-panel p-6">
      <h2 className="text-sm font-bold">{title}</h2>
      {children}
    </section>
  );
}

function SaveButton({ busy, children, disabled }) {
  return (
    <button
      type="submit"
      disabled={busy || disabled}
      className="mt-5 rounded-lg bg-panel-2 px-4 py-2 text-sm font-semibold text-ink transition-colors hover:text-hold disabled:cursor-not-allowed disabled:opacity-40"
    >
      {busy ? "Menyimpan..." : children}
    </button>
  );
}

function Profile({ user }) {
  const form = useForm({ name: user.name });
  const [saved, setSaved] = useState(false);
  const submit = async (e) => {
    e.preventDefault();
    setSaved(false);
    const r = await form.run(({ name }) => updateMe(name));
    if (r) {
      updateSessionUser(r.user);
      setSaved(true);
    }
  };
  const methods = [user.has_password && "Email + kata sandi", user.google_linked && "Google"].filter(Boolean).join(", ");
  return (
    <Section title="Profil">
      <dl className="mt-4 grid grid-cols-[8rem_1fr] gap-y-2 text-sm">
        <dt className="text-muted">Email</dt>
        <dd className="break-all">
          {user.email} {user.email_verified ? <span className="text-xs text-up">· terverifikasi</span> : <span className="text-xs text-hold">· belum terverifikasi</span>}
        </dd>
        <dt className="text-muted">Peran</dt>
        <dd>{user.role === "admin" ? "Admin" : "User"}</dd>
        <dt className="text-muted">Masuk lewat</dt>
        <dd>{methods || "-"}</dd>
        <dt className="text-muted">Terdaftar</dt>
        <dd>{fmtTime(user.created_at)}</dd>
      </dl>
      {user.managed && (
        <Notice>Akun bawaan untuk demo: dibuat dan kata sandinya diatur dari konfigurasi backend (.env / run.env).</Notice>
      )}
      <form onSubmit={submit} noValidate className="max-w-sm">
        <Field id="name" label="Nama" autoComplete="name" {...form.bind("name")} />
        {form.message && <Notice tone="error">{form.message.text}</Notice>}
        {saved && !form.message && <Notice tone="ok">Nama disimpan.</Notice>}
        <SaveButton busy={form.busy} disabled={form.values.name.trim() === user.name}>
          Simpan nama
        </SaveButton>
      </form>
    </Section>
  );
}

function Password({ user }) {
  const form = useForm({ current_password: "", password: "", password_confirm: "" });
  const [done, setDone] = useState(null);
  if (user.managed) {
    return (
      <Section title="Kata sandi">
        <p className="mt-3 text-sm text-muted">
          Kata sandi akun bawaan diatur lewat <code className="text-ink">ADMIN_PASSWORD</code> /{" "}
          <code className="text-ink">DEMO_USER_PASSWORD</code> di konfigurasi backend, bukan dari halaman ini.
        </p>
      </Section>
    );
  }
  const submit = async (e) => {
    e.preventDefault();
    setDone(null);
    const r = await form.run(changePassword);
    if (r) {
      updateSessionUser(r.user);
      form.setValues({ current_password: "", password: "", password_confirm: "" });
      setDone(r.message);
    }
  };
  return (
    <Section title={user.has_password ? "Ganti kata sandi" : "Buat kata sandi"}>
      {!user.has_password && (
        <p className="mt-3 text-sm text-muted">Akun ini masuk lewat Google. Buat kata sandi supaya bisa masuk dengan email juga.</p>
      )}
      <form onSubmit={submit} noValidate className="max-w-sm">
        {user.has_password && (
          <Field id="current_password" label="Kata sandi saat ini" type="password" autoComplete="current-password" {...form.bind("current_password")} />
        )}
        <Field
          id="password"
          label="Kata sandi baru"
          type="password"
          autoComplete="new-password"
          hint="Minimal 8 karakter, berisi huruf dan angka."
          {...form.bind("password")}
        />
        <Field id="password_confirm" label="Ulangi kata sandi baru" type="password" autoComplete="new-password" {...form.bind("password_confirm")} />
        {form.message && <Notice tone="error">{form.message.text}</Notice>}
        {done && <Notice tone="ok">{done}</Notice>}
        <SaveButton busy={form.busy}>{user.has_password ? "Ganti kata sandi" : "Buat kata sandi"}</SaveButton>
      </form>
    </Section>
  );
}

function Sessions({ expiresAt }) {
  const navigate = useNavigate();
  const [state, setState] = useState({ busy: false, error: null });
  const all = async () => {
    setState({ busy: true, error: null });
    try {
      await logoutAll();
      clearSession("logout");
      navigate("/login", { replace: true });
    } catch (err) {
      setState({ busy: false, error: err.message });
    }
  };
  return (
    <Section title="Sesi login">
      <p className="mt-3 text-sm text-muted">Sesi di perangkat ini berlaku sampai {fmtTime(expiresAt)}.</p>
      {state.error && <Notice tone="error">{state.error}</Notice>}
      <div className="mt-4 flex flex-wrap gap-3">
        <button
          onClick={() => signOut().then(() => navigate("/", { replace: true }))}
          className="rounded-lg border border-line px-4 py-2 text-xs font-semibold text-ink transition-colors hover:border-line-strong"
        >
          Keluar
        </button>
        <button
          onClick={all}
          disabled={state.busy}
          className="rounded-lg border border-line px-4 py-2 text-xs font-semibold text-down transition-colors hover:border-down disabled:opacity-40"
        >
          {state.busy ? "Memproses..." : "Keluar dari semua perangkat"}
        </button>
      </div>
    </Section>
  );
}

export default function AccountPage() {
  const { user, expiresAt } = useAuth();

  // Ambil profil terbaru (peran / status bisa diubah admin sejak login). 401 menghapus sesi.
  useEffect(() => {
    const ctrl = new AbortController();
    getMe(ctrl.signal)
      .then((r) => updateSessionUser(r.user))
      .catch(() => {});
    return () => ctrl.abort();
  }, []);

  if (!user) return null; // RequireLogin (App.jsx) mengarahkan ke /login

  return (
    <div className="mx-auto max-w-2xl">
      <h1 className="text-xl font-bold">Akun</h1>
      <p className="mt-1 text-xs text-muted">
        {user.name} · {user.role === "admin" ? (
          <Link to="/admin" className={linkClass}>
            buka halaman admin
          </Link>
        ) : (
          "pengguna"
        )}
      </p>
      <Profile key={`p-${user.id}`} user={user} />
      <Password key={`pw-${user.id}-${user.has_password}`} user={user} />
      <Sessions expiresAt={expiresAt} />
    </div>
  );
}
