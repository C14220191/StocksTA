// Tiga metode analisis AI (Hermes skills di .hermes/skills/trading-analysis). id sama dengan
// parameter ?method= di /api/analyze. Teks di sini hanya untuk tampilan UI.
export const METHODS = [
  {
    id: "debate",
    name: "Multi-Agent Debate (Bull vs Bear)",
    source: "TradingAgents dan ai-hedge-fund",
    fit: "Analisis menyeluruh: fundamental, sentimen, dan teknikal.",
    how: [
      "Pemisahan peran: analis fundamental, teknikal, dan sentimen hanya membaca bagian datanya sendiri, jadi satu sinyal kuat tidak mencemari penilaian aspek lain.",
      "Debat bull vs bear dua ronde: model dipaksa membantah dirinya sendiri, mengurangi kecenderungan mengikuti arah yang pertama ia ambil.",
      "Signal dan confidence per analis membuat hasil terukur dan bisa dibandingkan antar aset atau antar hari.",
    ],
    adapted: "Risk manager hanya menilai risiko dan volatilitas, tanpa ukuran posisi. Sentimen tidak tersedia karena sistem tidak mengambil berita.",
    weakness: "Prompt panjang dan boros token.",
  },
  {
    id: "mtf",
    name: "Structured Multi-Timeframe Technical Analysis",
    source: "Komunitas r/Trading dan r/CryptoCurrency",
    fit: "Analisis teknikal jangka pendek hingga menengah.",
    how: [
      "Top-down: timeframe 1D menentukan arah, 4H dan 1H hanya untuk timing, supaya sinyal 1H tidak melawan trend 1D.",
      "Confluence score 0–5 mengubah penilaian subjektif menjadi checklist; setup dianggap jelas hanya jika skornya minimal 4.",
      "Model boleh menyimpulkan tidak ada setup yang jelas, sehingga tidak memaksakan arah.",
      "Skenario bullish, bearish, dan level pembatal mencegah tunnel vision pada satu arah.",
    ],
    adapted: "Trade plan (entry, stop loss, take profit, risk/reward) diganti skenario dan level pembatal analisis.",
    weakness: "Hanya membaca harga dan indikator; fundamental dan berita tidak dipertimbangkan.",
  },
  {
    id: "grounded",
    name: "Grounded Research Brief + Devil's Advocate",
    source: "Gabungan praktik komunitas r/ChatGPT",
    fit: "Menguji ulang pandangan yang tersirat dari data.",
    how: [
      "Grounding: model hanya memakai data yang diberikan dan wajib menyebut sumber setiap angka (kode [D1]–[D10]).",
      "Label FAKTA / PROYEKSI / OPINI memisahkan yang sudah terjadi dari yang hanya diperkirakan.",
      "Devil's advocate membongkar 5 kelemahan terbesar pandangan dominan dan data apa yang akan membuktikannya salah.",
    ],
    adapted: "Thesis pengguna diganti pandangan dominan yang dibuat otomatis dari indikator teknikal.",
    weakness: "Hasilnya hanya sebagus data yang tersedia; berita tidak diambil sistem.",
  },
];

export const BIAS = {
  BUY: { label: "Bullish", cls: "text-up" },
  HOLD: { label: "Netral", cls: "text-hold" },
  SELL: { label: "Bearish", cls: "text-down" },
  MIXED: { label: "Beragam", cls: "text-muted" },
};

// detail.signal analis / detail.bias memakai bullish | bearish | neutral
export const BIAS_WORD = {
  bullish: { label: "Bullish", cls: "text-up" },
  neutral: { label: "Netral", cls: "text-hold" },
  bearish: { label: "Bearish", cls: "text-down" },
};

// Bias gabungan beberapa analisis: mayoritas, atau MIXED kalau berbeda (sama dengan backend).
export function consensus(signals) {
  const s = signals.filter(Boolean);
  if (!s.length) return null;
  const count = {};
  for (const x of s) {
    count[x] = (count[x] ?? 0) + 1;
    if (count[x] * 2 > s.length) return x;
  }
  return "MIXED";
}
