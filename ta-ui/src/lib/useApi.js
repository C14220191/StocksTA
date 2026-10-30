import { useCallback, useEffect, useState } from "react";

// Hook fetch kecil: batalkan request lama saat deps berubah.
// Saat reload/ganti parameter, data lama tetap tersedia selama loading (tidak berkedip kosong);
// saat error, data dikosongkan supaya tidak menampilkan data yang tidak sesuai parameter.
export default function useApi(fetcher, deps) {
  const [state, setState] = useState({ data: null, error: null, loading: true });
  const [tick, setTick] = useState(0);

  useEffect(() => {
    const ctrl = new AbortController();
    setState((s) => ({ data: s.data, error: null, loading: true }));
    fetcher(ctrl.signal)
      .then((data) => setState({ data, error: null, loading: false }))
      .catch((error) => {
        if (error.name !== "AbortError") setState({ data: null, error, loading: false });
      });
    return () => ctrl.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, tick]);

  const reload = useCallback(() => setTick((t) => t + 1), []);
  return { ...state, reload };
}

export function useDebounced(value, ms = 300) {
  const [v, setV] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setV(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return v;
}
