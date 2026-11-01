import { Routes, Route, useParams, Link, Outlet } from "react-router-dom";
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

// key={symbol} membuat state Detail Page di-reset penuh saat pindah instrumen
function DetailRoute() {
  const { symbol } = useParams();
  return <DetailPage key={symbol} symbol={symbol} />;
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
  return (
    <Routes>
      {/* Admin: tanpa autentikasi, backend hanya bisa diakses dari 127.0.0.1 */}
      <Route path="/admin" element={<AdminLayout />}>
        <Route index element={<DashboardPage />} />
        <Route path="prompt" element={<PromptPage />} />
        <Route path="cache" element={<CachePage />} />
        <Route path="evaluation" element={<EvaluationPage />} />
        <Route path="health" element={<HealthPage />} />
      </Route>
      <Route element={<UserLayout />}>
        <Route path="/" element={<HomePage />} />
        <Route path="/detail/:symbol" element={<DetailRoute />} />
        <Route path="/search" element={<SearchPage />} />
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
