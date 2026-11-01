// Skenario beban eksperimen live paralel (Bab 3.2 tahap 5: "semua kondisi pada skenario beban dan
// data pasar yang sama"). Setiap iterasi memilih satu instrument menurut distribusi popularitas,
// lalu mengirim request yang SAMA ke semua instance kondisi (.\ta-up.ps1 lab start) secara
// bersamaan, sehingga setiap kondisi menerima urutan request dan kondisi pasar yang identik.
//
// Pasang k6:  winget install k6 --source winget
// Jalankan :  k6 run loadtest/lab.js
//             k6 run -e RATE=12 -e DURATION=24h loadtest/lab.js
//
// Parameter (-e NAMA=nilai):
//   RATE      iterasi per menit (default 6 = satu request tiap 10 detik ke setiap kondisi)
//   DURATION  lama uji, format k6 (default 1h; eksperimen utama mis. 72h atau 168h)
//   PORTS     daftar port dipisah koma (default 8101-8115, sama dengan matriks lab)
//   HOST      default 127.0.0.1 (jangan "localhost": di Windows menambah ~200 ms lewat IPv6)
//   MIX       distribusi popularitas "SIMBOL:bobot,..." (default di bawah)
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

export const options = {
  scenarios: {
    lab: {
      executor: "constant-arrival-rate",
      rate: Number(__ENV.RATE || 6),
      timeUnit: "1m",
      duration: __ENV.DURATION || "1h",
      preAllocatedVUs: 20,
      maxVUs: 200,
    },
  },
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
  const params = { headers: { "X-TA-Trigger": "k6" }, timeout: "400s", tags: { symbol } };
  const responses = http.batch(
    PORTS.map((port) => ["GET", `http://${HOST}:${port}/api/analyze/${symbol}`, null, { ...params, tags: { symbol, port } }])
  );
  responses.forEach((res, i) =>
    check(res, { [`${PORTS[i]} status 200`]: (r) => r.status === 200 })
  );
}
