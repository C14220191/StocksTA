import { useEffect, useState } from "react";
import { AUTH_EVENT, getSession } from "./auth.js";

// Akun yang sedang login (atau null), ikut berubah saat login / logout / profil diperbarui, termasuk
// dari tab lain. reason = alasan perubahan terakhir ("expired" kalau sesi ditolak backend).
export default function useAuth() {
  const [state, setState] = useState(() => ({ session: getSession(), reason: null }));

  useEffect(() => {
    const onAuth = (e) => setState({ session: getSession(), reason: e.detail ?? null });
    const onStorage = () => setState((s) => ({ ...s, session: getSession() }));
    window.addEventListener(AUTH_EVENT, onAuth);
    window.addEventListener("storage", onStorage);
    return () => {
      window.removeEventListener(AUTH_EVENT, onAuth);
      window.removeEventListener("storage", onStorage);
    };
  }, []);

  return { user: state.session?.user ?? null, expiresAt: state.session?.expires_at ?? null, reason: state.reason };
}
