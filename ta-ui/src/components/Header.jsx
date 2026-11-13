import { useEffect, useRef, useState } from "react";
import { Link, useLocation, useNavigate, useSearchParams } from "react-router-dom";
import { signOut } from "../lib/api.js";
import { safeNext } from "../lib/auth.js";
import useAuth from "../lib/useAuth.js";

// Menu akun di kanan header: Masuk / Daftar, atau nama akun (ke /account), Admin, dan Keluar.
function AccountNav() {
  const { user } = useAuth();
  const { pathname, search } = useLocation();
  const next = safeNext(pathname + search, "");
  const withNext = (path) => (next && next !== "/" ? `${path}?next=${encodeURIComponent(next)}` : path);
  const link = "whitespace-nowrap text-sm font-semibold text-muted transition-colors hover:text-ink";

  if (!user) {
    return (
      <nav aria-label="Akun" className="flex shrink-0 items-center gap-4">
        <Link to={withNext("/login")} className={link}>
          Masuk
        </Link>
        <Link
          to={withNext("/register")}
          className="whitespace-nowrap border border-line px-3 py-1.5 text-sm font-semibold hover:border-line-strong"
        >
          Daftar
        </Link>
      </nav>
    );
  }
  return (
    <nav aria-label="Akun" className="flex shrink-0 items-center gap-4">
      {user.role === "admin" && (
        <Link to="/admin" className={link}>
          Admin
        </Link>
      )}
      <Link to="/account" className={`${link} max-w-40 truncate text-ink`} title={user.email}>
        {user.name}
      </Link>
      <button onClick={signOut} className={link}>
        Keluar
      </button>
    </nav>
  );
}

// Kotak pencarian global: tekan Enter -> halaman /search?q=...
export default function Header() {
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const [params] = useSearchParams();
  const onSearch = pathname === "/search";
  const q = onSearch ? (params.get("q") ?? "") : "";
  const [input, setInput] = useState(q);
  const ref = useRef(null);
  const { user } = useAuth();

  // Ikuti URL: isi kotak dengan kata kunci di /search, kosongkan saat pindah halaman lain.
  // Di /search tanpa kata kunci (mis. dari tautan "Cari instrumen lain"), langsung fokus ke kotak.
  useEffect(() => {
    setInput(q);
    if (onSearch && !q) ref.current?.focus();
  }, [q, onSearch]);

  const submit = (e) => {
    e.preventDefault();
    const term = input.trim();
    if (term) navigate(`/search?q=${encodeURIComponent(term)}`);
  };

  return (
    <header className="border-b border-line-strong bg-surface">
      <div className="mx-auto flex max-w-6xl flex-wrap items-center justify-between gap-x-6 gap-y-4 px-6 py-6 md:flex-nowrap md:px-10">
        <Link to="/" className="shrink-0 text-4xl font-bold tracking-tight">
          Branding
        </Link>
        {/* Pencarian hanya setelah login (halaman data wajib login) */}
        {user && (
          <form role="search" onSubmit={submit} className="relative order-last w-full md:order-none md:max-w-sm">
            <svg
              aria-hidden="true"
              viewBox="0 0 24 24"
              className="pointer-events-none absolute left-4 top-1/2 h-4 w-4 -translate-y-1/2 text-muted"
              fill="none"
              stroke="currentColor"
              strokeWidth="2.5"
            >
              <circle cx="11" cy="11" r="7" />
              <path d="m20 20-3.5-3.5" />
            </svg>
            <input
              ref={ref}
              type="search"
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={(e) => e.key === "Escape" && setInput("")}
              placeholder="Cari saham, kripto, atau emas, tekan Enter"
              aria-label="Cari instrumen"
              enterKeyHint="search"
              className="w-full border border-line bg-panel py-2 pl-11 pr-4 text-lg font-bold outline-none transition-colors placeholder:text-sm placeholder:font-normal placeholder:text-muted hover:border-line-strong focus:border-line-strong"
            />
          </form>
        )}
        <AccountNav />
      </div>
    </header>
  );
}
