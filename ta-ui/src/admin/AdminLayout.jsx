import { Link, NavLink, Outlet } from "react-router-dom";

const NAV = [
  { to: "/admin", label: "Dashboard", end: true },
  { to: "/admin/prompt", label: "Prompt" },
  { to: "/admin/cache", label: "Cache & Job" },
  { to: "/admin/evaluation", label: "Evaluasi" },
  { to: "/admin/health", label: "API Health" },
];

// Layout admin: sidebar di layar lebar, menu horizontal di layar sempit.
export default function AdminLayout() {
  return (
    <div className="min-h-screen font-sans md:flex">
      <aside className="border-b border-line bg-canvas md:sticky md:top-0 md:h-screen md:w-56 md:shrink-0 md:border-b-0 md:border-r">
        <div className="px-5 py-5">
          <div className="text-lg font-bold tracking-tight">Admin</div>
          <div className="text-xs text-muted">Cached AI Inference</div>
        </div>
        <nav className="flex gap-1 overflow-x-auto px-3 pb-3 md:flex-col md:overflow-visible">
          {NAV.map((n) => (
            <NavLink
              key={n.to}
              to={n.to}
              end={n.end}
              className={({ isActive }) =>
                `whitespace-nowrap rounded-lg px-3 py-2 text-sm font-semibold transition-colors ${
                  isActive ? "bg-panel-2 text-ink" : "text-muted hover:text-ink"
                }`
              }
            >
              {n.label}
            </NavLink>
          ))}
        </nav>
        <div className="hidden px-5 pt-6 md:block">
          <Link to="/" className="text-xs text-muted hover:text-ink">
            ← Ke aplikasi pengguna
          </Link>
        </div>
      </aside>
      <main className="min-w-0 flex-1 px-4 py-8 md:px-10">
        <div className="mx-auto max-w-7xl">
          <Outlet />
        </div>
      </main>
    </div>
  );
}
