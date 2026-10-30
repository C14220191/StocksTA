import { Link } from "react-router-dom";
import { formatPrice, formatChange, changeClass } from "../lib/format.js";

export default function MoverCard({ title, rows, empty }) {
  return (
    <section className="flex-1">
      <h2 className="mb-5 text-lg font-bold">{title}</h2>
      <div className="divide-y divide-line rounded-xl border border-line bg-panel">
        {!rows?.length && <p className="px-6 py-5 text-sm text-muted">{empty}</p>}
        {rows?.map((r) => (
          <Link
            key={r.symbol}
            to={`/detail/${encodeURIComponent(r.symbol)}`}
            className="flex items-center justify-between gap-3 px-6 py-5 transition-colors first:rounded-t-xl last:rounded-b-xl hover:bg-panel-2"
          >
            <div className="min-w-0 flex-1">
              <div className="font-bold">{r.display_symbol}</div>
              <div className="truncate text-xs text-muted">{r.name}</div>
            </div>
            <div className="font-bold">{formatPrice(r.price, r.currency)}</div>
            <div className={`w-20 text-right font-bold ${changeClass(r.change_pct)}`}>{formatChange(r.change_pct)}</div>
          </Link>
        ))}
      </div>
    </section>
  );
}
