import { useEffect, useState } from "react";
import useApi from "../../lib/useApi.js";
import useAuth from "../../lib/useAuth.js";
import { createUser, deleteUser, getAdminUsers, sendUserPasswordReset, updateUser } from "../../lib/api.js";
import { Field, Notice } from "../../components/AuthForm.jsx";
import useForm from "../../lib/useForm.js";
import { ErrorBox, PageHeader, Panel, Segmented, StatusDot, Table, Td, fmtDateTime, fmtInt } from "../ui.jsx";

const PAGE = 50;
const ROLES = [
  { value: "", label: "Semua" },
  { value: "admin", label: "Admin" },
  { value: "user", label: "User" },
];

function Chip({ children, tone = "text-muted" }) {
  return <span className={`ml-2 rounded-full border border-line px-2 py-0.5 text-[10px] font-semibold ${tone}`}>{children}</span>;
}

const smallBtn =
  "rounded-md border border-line px-2.5 py-1 text-[11px] font-semibold transition-colors hover:border-line-strong disabled:cursor-not-allowed disabled:opacity-40";

function CreateUser({ onCreated }) {
  const form = useForm({ name: "", email: "", password: "", role: "user" });
  const submit = async (e) => {
    e.preventDefault();
    const r = await form.run(createUser);
    if (r) {
      form.setValues({ name: "", email: "", password: "", role: "user" });
      onCreated(r.user);
    }
  };
  const role = form.bind("role");
  return (
    <Panel title="Tambah pengguna" aside="Email divalidasi seperti pendaftaran; akun langsung terverifikasi">
      <form onSubmit={submit} noValidate className="grid gap-x-4 md:grid-cols-2">
        <Field id="new-name" label="Nama" autoComplete="off" {...form.bind("name")} className="mt-0!" />
        <Field id="new-email" label="Email" type="email" autoComplete="off" {...form.bind("email")} className="md:mt-0!" />
        <Field
          id="new-password"
          label="Kata sandi awal"
          type="password"
          autoComplete="new-password"
          hint="Minimal 8 karakter, huruf dan angka. Sampaikan ke pemilik akun lewat jalur aman."
          {...form.bind("password")}
        />
        <div className="mt-4">
          <label htmlFor="new-role" className="block text-xs font-semibold text-muted">
            Peran
          </label>
          <select
            id="new-role"
            value={role.value}
            onChange={role.onChange}
            className="mt-2 w-full rounded-lg border border-line bg-canvas px-3 py-2 text-sm text-ink outline-none focus:border-line-strong"
          >
            <option value="user">User</option>
            <option value="admin">Admin</option>
          </select>
          {role.error && <p className="mt-1.5 text-xs text-down">{role.error}</p>}
        </div>
        <div className="md:col-span-2">
          {form.message && <Notice tone="error">{form.message.text}</Notice>}
          <button
            type="submit"
            disabled={form.busy}
            className="mt-5 rounded-lg bg-panel-2 px-4 py-2 text-xs font-semibold text-ink hover:text-hold disabled:opacity-40"
          >
            {form.busy ? "Menyimpan..." : "Tambah pengguna"}
          </button>
        </div>
      </form>
    </Panel>
  );
}

function UserRow({ u, me, onChanged, onMessage }) {
  const [busy, setBusy] = useState(null); // nama aksi yang sedang berjalan
  const self = u.id === me.id;
  const lockedAdmin = self || (u.managed && u.role === "admin"); // tidak bisa diturunkan / dinonaktifkan / dihapus

  const act = async (name, fn, okText) => {
    setBusy(name);
    try {
      const r = await fn();
      onMessage({ tone: "ok", text: typeof okText === "function" ? okText(r) : okText });
      onChanged();
    } catch (err) {
      onMessage({ tone: "error", text: `${u.email}: ${err.fields ? Object.values(err.fields)[0] ?? err.message : err.message}` });
    } finally {
      setBusy(null);
    }
  };

  const methods = [u.has_password && "Kata sandi", u.google_linked && "Google"].filter(Boolean).join(" + ") || "-";
  return (
    <tr>
      <Td>
        <span className="font-semibold text-ink">{u.name}</span>
        {self && <Chip tone="text-hold">Anda</Chip>}
        {u.managed && <Chip>bawaan</Chip>}
      </Td>
      <Td>
        <div className="break-all">{u.email}</div>
        <div className={`mt-0.5 text-[11px] ${u.email_verified ? "text-up" : "text-hold"}`}>
          {u.email_verified ? "terverifikasi" : "belum terverifikasi"}
        </div>
      </Td>
      <Td>
        <select
          aria-label={`Peran ${u.email}`}
          value={u.role}
          disabled={busy !== null || (lockedAdmin && u.role === "admin")}
          onChange={(e) => {
            const role = e.target.value;
            act("role", () => updateUser(u.id, { role }), `${u.email} sekarang berperan ${role}.`);
          }}
          className="rounded-md border border-line bg-panel px-2 py-1 text-xs font-semibold text-ink outline-none focus:border-line-strong disabled:opacity-50"
        >
          <option value="user">User</option>
          <option value="admin">Admin</option>
        </select>
      </Td>
      <Td>{methods}</Td>
      <Td>{u.disabled ? <StatusDot status="down" label="Nonaktif" /> : <StatusDot status="ok" label="Aktif" />}</Td>
      <Td>{fmtDateTime(u.created_at)}</Td>
      <Td>{fmtDateTime(u.last_login_at)}</Td>
      <Td>
        <div className="flex flex-wrap gap-1.5">
          {!u.email_verified && (
            <button
              className={smallBtn}
              disabled={busy !== null}
              onClick={() => act("verify", () => updateUser(u.id, { email_verified: true }), `Email ${u.email} ditandai terverifikasi.`)}
            >
              Tandai terverifikasi
            </button>
          )}
          {!u.managed && !u.disabled && (
            <button
              className={smallBtn}
              disabled={busy !== null}
              onClick={() =>
                act("reset", () => sendUserPasswordReset(u.id), (r) =>
                  r.mail === "log" ? `${r.message} SMTP belum diatur: link ada di log backend.` : r.message
                )
              }
            >
              {busy === "reset" ? "Mengirim..." : "Kirim link reset"}
            </button>
          )}
          {!lockedAdmin && (
            <button
              className={smallBtn}
              disabled={busy !== null}
              onClick={() =>
                act(
                  "disable",
                  () => updateUser(u.id, { disabled: !u.disabled }),
                  u.disabled ? `${u.email} diaktifkan lagi.` : `${u.email} dinonaktifkan; semua sesinya dikeluarkan.`
                )
              }
            >
              {u.disabled ? "Aktifkan" : "Nonaktifkan"}
            </button>
          )}
          {!lockedAdmin && (
            <button
              className={`${smallBtn} text-down hover:border-down`}
              disabled={busy !== null}
              onClick={() => {
                if (window.confirm(`Hapus akun ${u.email}? Tindakan ini tidak bisa dibatalkan.`)) {
                  act("delete", () => deleteUser(u.id), `Akun ${u.email} dihapus.`);
                }
              }}
            >
              Hapus
            </button>
          )}
        </div>
      </Td>
    </tr>
  );
}

export default function UsersPage() {
  const { user: me } = useAuth();
  const [input, setInput] = useState("");
  const [q, setQ] = useState("");
  const [role, setRole] = useState("");
  const [offset, setOffset] = useState(0);
  const [message, setMessage] = useState(null);
  const [showCreate, setShowCreate] = useState(false);

  // cari setelah berhenti mengetik sebentar
  useEffect(() => {
    const t = setTimeout(() => {
      setQ(input.trim());
      setOffset(0);
    }, 300);
    return () => clearTimeout(t);
  }, [input]);

  const { data, error, loading, reload } = useApi(
    (signal) => getAdminUsers({ q, role, limit: PAGE, offset }, signal),
    [q, role, offset]
  );
  const users = data?.users ?? [];
  const total = data?.total ?? 0;

  return (
    <div className="space-y-6">
      <PageHeader
        title="Pengguna"
        subtitle="Akun, peran (admin / user), status verifikasi email. Akun bawaan dibuat dari konfigurasi backend (ADMIN_EMAIL, DEMO_USER_EMAIL)."
      >
        <button
          onClick={() => setShowCreate((v) => !v)}
          className="rounded-lg bg-panel-2 px-4 py-2 text-xs font-semibold text-ink hover:text-hold"
        >
          {showCreate ? "Tutup form" : "Tambah pengguna"}
        </button>
      </PageHeader>

      {showCreate && (
        <CreateUser
          onCreated={(u) => {
            setMessage({ tone: "ok", text: `Akun ${u.email} dibuat (${u.role}).` });
            setShowCreate(false);
            reload();
          }}
        />
      )}

      <div className="flex flex-wrap items-center gap-3">
        <input
          type="search"
          value={input}
          onChange={(e) => setInput(e.target.value)}
          placeholder="Cari nama atau email"
          aria-label="Cari pengguna"
          className="w-full max-w-xs rounded-lg border border-line bg-panel px-3 py-2 text-xs text-ink outline-none focus:border-line-strong"
        />
        <Segmented
          label="Filter peran"
          value={role}
          options={ROLES}
          onChange={(v) => {
            setRole(v);
            setOffset(0);
          }}
        />
        <span className="text-xs text-muted">{loading ? "Memuat..." : `${fmtInt(total)} akun`}</span>
      </div>

      {message && (
        <div
          role={message.tone === "error" ? "alert" : "status"}
          className={`flex items-start justify-between gap-4 rounded-lg bg-panel-2 px-4 py-3 text-xs ${message.tone === "error" ? "text-down" : "text-up"}`}
        >
          <span>{message.text}</span>
          <button onClick={() => setMessage(null)} aria-label="Tutup pesan" className="text-muted hover:text-ink">
            ✕
          </button>
        </div>
      )}

      <ErrorBox error={error} onRetry={reload} />
      {!error && me && (
        <Panel>
          <Table
            head={["Nama", "Email", "Peran", "Masuk lewat", "Status", "Terdaftar", "Login terakhir", "Aksi"]}
            empty={!loading && users.length === 0 ? (q || role ? "Tidak ada akun yang cocok." : "Belum ada akun.") : null}
          >
            {users.map((u) => (
              <UserRow key={u.id} u={u} me={me} onChanged={reload} onMessage={setMessage} />
            ))}
          </Table>
          {total > PAGE && (
            <div className="mt-4 flex items-center justify-end gap-3 text-xs text-muted">
              <span>
                {fmtInt(offset + 1)}–{fmtInt(Math.min(offset + PAGE, total))} dari {fmtInt(total)}
              </span>
              <button className={smallBtn} disabled={offset === 0} onClick={() => setOffset(Math.max(offset - PAGE, 0))}>
                Sebelumnya
              </button>
              <button className={smallBtn} disabled={offset + PAGE >= total} onClick={() => setOffset(offset + PAGE)}>
                Berikutnya
              </button>
            </div>
          )}
        </Panel>
      )}
    </div>
  );
}
