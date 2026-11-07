// Skenario beban eksperimen live paralel (Bab 3.2 tahap 5: "semua kondisi pada skenario beban dan
// data pasar yang sama"). Setiap iterasi = satu kunjungan halaman Detail: satu instrument dipilih
// menurut distribusi popularitas, lalu request untuk SETIAP metode analisis (debate, mtf, grounded)
// dikirim ke semua instance kondisi (.\ta-up.ps1 lab start) secara bersamaan, sehingga setiap
// kondisi menerima urutan request dan kondisi pasar yang identik.
//
// Eksperimen utama (server): ./ta-up.sh bench start   -> lab stub + k6 7 hari di background, MIX Zipf
// atas semua instrumen, jendela run dicatat di logs/bench/. Lihat docs/CHECKPOINT.md "Benchmark 1 minggu".
//
// Manual:     k6 run loadtest/lab.js
//             k6 run -e RATE=12 -e DURATION=24h loadtest/lab.js
// Pasang k6:  winget install k6 --source winget (Windows); di server bench memakai image docker grafana/k6
//
// Parameter (-e NAMA=nilai):
//   RATE      iterasi per menit (default 6 = satu request tiap 10 detik ke setiap kondisi)
//   DURATION  lama uji, format k6 (default 1h; eksperimen utama 168h = 1 minggu, lewat bench)
//   PORTS     daftar port dipisah koma (default 8101-8115, sama dengan matriks lab)
//   HOST      default 127.0.0.1 (jangan "localhost": di Windows menambah ~200 ms lewat IPv6)
//   MIX       distribusi popularitas "SIMBOL:bobot,..." (default di bawah; bench mengisi Zipf)
//   METHODS   metode analisis per kunjungan, dipisah koma (default debate,mtf,grounded)
import http from "k6/http";
import { check } from "k6";

const HOST = __ENV.HOST || "127.0.0.1";
const PORTS = (__ENV.PORTS || "8101,8102,8103,8104,8105,8106,8107,8108,8109,8110,8111,8112,8113,8114,8115")
  .split(",")
  .map((p) => p.trim());
const MIX = (__ENV.MIX || "BTCUSDT:40,ETHUSDT:25,XAUUSDT:15,BBCA.JK:10,BBRI.JK:5,TLKM.JK:5")
  .split(",")
  .map((x) => {
    const [symbol, w] = x.split(":");
    return { symbol, w: Number(w) };
  });
const TOTAL_W = MIX.reduce((s, m) => s + m.w, 0);
const METHODS = (__ENV.METHODS || "debate,mtf,grounded").split(",").map((m) => m.trim());

export const options = {
  scenarios: {
    lab: {
      executor: "constant-arrival-rate",
      rate: Number(__ENV.RATE || 6),
      timeUnit: "1m",
      duration: __ENV.DURATION || "1h",
      preAllocatedVUs: 20,
      maxVUs: 200,
      // Kunjungan terakhir dibiarkan selesai (AI no_cache bisa puluhan detik), bukan diputus di 30 dtk.
      gracefulStop: "6m",
    },
  },
  // Isi respons tidak dipakai (cek hanya status); tanpa ini k6 menahan ratusan ribu body selama seminggu.
  discardResponseBodies: true,
  // Semua request satu kunjungan (15 port x 3 metode = 45) harus berangkat bersamaan supaya setiap kondisi
  // menerima request pada saat dan kondisi pasar yang sama. Default k6 hanya 10 paralel per batch (6 per
  // host): dengan latency AI realistis (stub profil pilot 12-22 dtk) sisanya antre puluhan detik.
  batch: 64,
  batchPerHost: 64,
  // Latency yang dianalisis adalah latency_ms di inference_log (diukur di backend); metrik k6
  // hanya pelengkap. Request lama (no_cache dengan AI sungguhan) jangan diputus.
  httpDebug: "",
  summaryTrendStats: ["avg", "p(50)", "p(95)", "p(99)", "max"],
};

function pickSymbol() {
  let r = Math.random() * TOTAL_W;
  for (const m of MIX) {
    if ((r -= m.w) <= 0) return m.symbol;
  }
  return MIX[MIX.length - 1].symbol;
}

export default function () {
  const symbol = pickSymbol();
  const params = { headers: { "X-TA-Trigger": "k6" }, timeout: "400s" };
  const reqs = [];
  for (const port of PORTS) {
    for (const method of METHODS) {
      const url = `http://${HOST}:${port}/api/analyze/${symbol}?method=${method}`;
      reqs.push({ port, method, req: ["GET", url, null, { ...params, tags: { symbol, port, method } }] });
    }
  }
  const responses = http.batch(reqs.map((r) => r.req));
  responses.forEach((res, i) =>
    check(res, { [`${reqs[i].port} ${reqs[i].method} status 200`]: (r) => r.status === 200 })
  );
}
