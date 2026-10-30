import { useEffect, useRef, useState } from "react";
import { Link, useLocation, useNavigate, useSearchParams } from "react-router-dom";

// Kotak pencarian global: tekan Enter -> halaman /search?q=...
export default function Header() {
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const [params] = useSearchParams();
  const onSearch = pathname === "/search";
  const q = onSearch ? params.get("q") ?? "" : "";
  const [input, setInput] = useState(q);
  const ref = useRef(null);

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
      <div className="mx-auto flex max-w-6xl items-center justify-between gap-6 px-6 py-6 md:px-10">
        <Link to="/" className="shrink-0 text-4xl font-bold tracking-tight">
          Branding
        </Link>
        <form role="search" onSubmit={submit} className="relative w-full max-w-sm">
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
            placeholder="Cari saham atau kripto, tekan Enter"
            aria-label="Cari instrumen"
            enterKeyHint="search"
            className="w-full border border-line bg-panel py-2 pl-11 pr-4 text-lg font-bold outline-none transition-colors placeholder:text-sm placeholder:font-normal placeholder:text-muted hover:border-line-strong focus:border-line-strong"
          />
        </form>
      </div>
    </header>
  );
}
