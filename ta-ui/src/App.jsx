import { Routes, Route, useParams, Link } from "react-router-dom";
import Header from "./components/Header.jsx";
import HomePage from "./pages/HomePage.jsx";
import DetailPage from "./pages/DetailPage.jsx";
import SearchPage from "./pages/SearchPage.jsx";

// key={symbol} membuat state Detail Page di-reset penuh saat pindah instrumen
function DetailRoute() {
  const { symbol } = useParams();
  return <DetailPage key={symbol} symbol={symbol} />;
}

export default function App() {
  return (
    <>
      <Header />
      <main className="mx-auto max-w-6xl px-6 pb-16 pt-10 md:px-10">
        <Routes>
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
        </Routes>
      </main>
    </>
  );
}
