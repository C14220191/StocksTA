// DATA DUMMY untuk mockup. Nanti diganti fetch ke Go backend.
const now = Date.now();

export const instruments = [
  { id: "GOTO", symbol: "GOTO", name: "GoTo Gojek Tokopedia", market: "idx", currency: "IDR", price: 82, change: 4.2, volume: 1_800_000, signal: "Buy", rsi: 61.4, support: 76, resistance: 89, generatedAt: now - 12 * 60000 },
  { id: "BBRI", symbol: "BBRI", name: "Bank Rakyat Indonesia", market: "idx", currency: "IDR", price: 4350, change: -1.1, volume: 642_000, signal: "Hold", rsi: 48.2, support: 4200, resistance: 4500, generatedAt: now - 30 * 60000 },
  { id: "BTCUSDT", symbol: "BTC/USDT", name: "Bitcoin", market: "crypto", currency: "USD", price: 84676, change: 2.6, volume: 2_100_000_000, signal: "Buy", rsi: 58.9, support: 82000, resistance: 87500, generatedAt: now - 5 * 60000 },
  { id: "TLKM", symbol: "TLKM", name: "Telkom Indonesia", market: "idx", currency: "IDR", price: 2910, change: 0.7, volume: 318_000, signal: "Hold", rsi: 52.0, support: 2800, resistance: 3000, generatedAt: now - 45 * 60000 },
  { id: "SOLUSDT", symbol: "SOL/USDT", name: "Solana", market: "crypto", currency: "USD", price: 212.4, change: 4.2, volume: 410_000_000, signal: "Buy", rsi: 64.3, support: 198, resistance: 225, generatedAt: now - 8 * 60000 },
  { id: "ASII", symbol: "ASII", name: "Astra International", market: "idx", currency: "IDR", price: 5125, change: 6.3, volume: 520_000, signal: "Buy", rsi: 67.5, support: 4800, resistance: 5300, generatedAt: now - 20 * 60000 },
  { id: "ANTM", symbol: "ANTM", name: "Aneka Tambang", market: "idx", currency: "IDR", price: 1560, change: -5.4, volume: 1_200_000, signal: "Hold", rsi: 39.8, support: 1500, resistance: 1650, generatedAt: now - 15 * 60000 },
  { id: "DOGEUSDT", symbol: "DOGE", name: "Dogecoin", market: "crypto", currency: "USD", price: 0.184, change: -4.7, volume: 95_000_000, signal: "Hold", rsi: 41.1, support: 0.17, resistance: 0.2, generatedAt: now - 10 * 60000 },
  { id: "ANTAM-W", symbol: "ANTAM-W", name: "Waran seri I Antam", market: "idx", currency: "IDR", price: 4350, change: -1.1, volume: 642_000, signal: "Hold", rsi: 45.0, support: 4100, resistance: 4500, generatedAt: now - 25 * 60000 },
  { id: "ETHUSDT", symbol: "ETH/USDT", name: "Ethereum", market: "crypto", currency: "USD", price: 3120, change: 1.4, volume: 1_100_000_000, signal: "Hold", rsi: 55.0, support: 3000, resistance: 3250, generatedAt: now - 6 * 60000 },
  { id: "BMRI", symbol: "BANK MANDIRI", name: "Bank Mandiri", market: "idx", currency: "IDR", price: 5800, change: 0.9, volume: 410_000, signal: "Hold", rsi: 51.0, support: 5600, resistance: 6000, generatedAt: now - 35 * 60000 },
  { id: "PGAS", symbol: "PGAS", name: "Perusahaan Gas Negara", market: "idx", currency: "IDR", price: 1650, change: 0.3, volume: 280_000, signal: "Hold", rsi: 49.0, support: 1600, resistance: 1720, generatedAt: now - 40 * 60000 },
];

export const aiSummary =
  "Lorem ipsum dolor sit amet, consectetur adipiscing elit. Sed rhoncus efficitur elit quis dignissim. Aliquam posuere urna in interdum interdum. Vivamus et metus id felis interdum dictum. Quisque nisi ex, facilisis at dui dignissim, imperdiet iaculis ligula. Nullam ut egestas nunc, dignissim accumsan metus.";

// Isi tiap bagian di Home (id instrumen). Nanti dihitung oleh backend.
export const homeSections = {
  topVolume: ["GOTO", "BBRI", "BTCUSDT", "TLKM"],
  gainers: ["SOLUSDT", "ASII"],
  losers: ["ANTM", "DOGEUSDT"],
};

export const recentSearches = [
  { label: "ETH/USDT", id: "ETHUSDT" },
  { label: "BANK MANDIRI", id: "BMRI" },
  { label: "PGAS", id: "PGAS" },
];

export const getInstrument = (id) => instruments.find((i) => i.id === id);
