import { Link } from "react-router-dom";

export default function Header() {
  return (
    <header className="border-b border-line-strong bg-surface">
      <div className="mx-auto flex max-w-6xl items-center justify-between px-6 py-6 md:px-10">
        <Link to="/" className="text-4xl font-bold tracking-tight">
          Branding
        </Link>
        <Link
          to="/search"
          className="border border-line bg-panel px-8 py-2 text-xl font-bold transition-colors hover:border-line-strong"
        >
          Search
        </Link>
      </div>
    </header>
  );
}
