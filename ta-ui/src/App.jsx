import { useEffect } from "react";
import { Routes, Route, useParams, Link, Navigate, Outlet, useLocation } from "react-router-dom";
import Header from "./components/Header.jsx";
import HomePage from "./pages/HomePage.jsx";
import DetailPage from "./pages/DetailPage.jsx";
import SearchPage from "./pages/SearchPage.jsx";
import AdminLayout from "./admin/AdminLayout.jsx";
import DashboardPage from "./admin/pages/DashboardPage.jsx";
import PromptPage from "./admin/pages/PromptPage.jsx";
import CachePage from "./admin/pages/CachePage.jsx";
import EvaluationPage from "./admin/pages/EvaluationPage.jsx";
import HealthPage from "./admin/pages/HealthPage.jsx";
import InstrumentsPage from "./admin/pages/InstrumentsPage.jsx";
import UsersPage from "./admin/pages/UsersPage.jsx";
import LoginPage from "./pages/auth/LoginPage.jsx";
import RegisterPage from "./pages/auth/RegisterPage.jsx";
import ForgotPasswordPage from "./pages/auth/ForgotPasswordPage.jsx";
import ResetPasswordPage from "./pages/auth/ResetPasswordPage.jsx";
import VerifyEmailPage from "./pages/auth/VerifyEmailPage.jsx";
import GoogleCallbackPage from "./pages/auth/GoogleCallbackPage.jsx";
import AccountPage from "./pages/auth/AccountPage.jsx";
import { getMe } from "./lib/api.js";
import { getToken, updateSessionUser } from "./lib/auth.js";
import useAuth from "./lib/useAuth.js";

// key={symbol} membuat state Detail Page di-reset penuh saat pindah instrumen
function DetailRoute() {
  const { symbol } = useParams();
  return <DetailPage key={symbol} symbol={symbol} />;
}

// Halaman data (Home, Search, Detail) dan Akun wajib login; tanpa sesi diarahkan ke /login lalu kembali.
function RequireLogin() {
  const { user, reason } = useAuth();
  const { pathname, search } = useLocation();
  if (user) return <Outlet />;
  const q = new URLSearchParams();
  if (pathname + search !== "/") q.set("next", pathname + search);
  if (reason === "expired") q.set("reason", "expired");
  const qs = q.toString();
  return <Navigate to={`/login${qs ? `?${qs}` : ""}`} replace />;
}

function UserLayout() {
  return (
    <>
      <Header />
      <main className="mx-auto max-w-6xl px-6 pb-16 pt-10 md:px-10">
        <Outlet />
      </main>
    </>
  );
}

export default function App() {
  // Sesi tersimpan dari kunjungan sebelumnya: segarkan profil (peran bisa diubah admin). 401 = sesi
  // dicabut atau habis, dihapus oleh lib/api.js.
  useEffect(() => {
    if (!getToken()) return;
    getMe()
      .then((r) => updateSessionUser(r.user))
      .catch(() => {});
  }, []);

  return (
    <Routes>
      {/* Admin: wajib login dengan akun ber-role admin; AdminLayout mengalihkan ke /login */}
      <Route path="/admin/login" element={<Navigate to="/login?next=%2Fadmin" replace />} />
      <Route path="/admin" element={<AdminLayout />}>
        <Route index element={<DashboardPage />} />
        <Route path="prompt" element={<PromptPage />} />
        <Route path="cache" element={<CachePage />} />
        <Route path="instruments" element={<InstrumentsPage />} />
        <Route path="users" element={<UsersPage />} />
        <Route path="evaluation" element={<EvaluationPage />} />
        <Route path="health" element={<HealthPage />} />
      </Route>
      <Route element={<UserLayout />}>
        <Route element={<RequireLogin />}>
          <Route path="/" element={<HomePage />} />
          <Route path="/detail/:symbol" element={<DetailRoute />} />
          <Route path="/search" element={<SearchPage />} />
          <Route path="/account" element={<AccountPage />} />
        </Route>
        <Route path="/login" element={<LoginPage />} />
        <Route path="/register" element={<RegisterPage />} />
        <Route path="/forgot-password" element={<ForgotPasswordPage />} />
        <Route path="/reset-password" element={<ResetPasswordPage />} />
        <Route path="/verify-email" element={<VerifyEmailPage />} />
        <Route path="/auth/google" element={<GoogleCallbackPage />} />
        <Route
          path="*"
          element={
            <p className="text-muted">
              Halaman tidak ditemukan. <Link to="/" className="underline">Kembali ke beranda</Link>
            </p>
          }
        />
      </Route>
    </Routes>
  );
}
