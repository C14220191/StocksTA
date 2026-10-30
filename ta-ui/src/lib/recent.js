// "Pencarian terakhir" disimpan di browser (sesuai dokumentasi API, tidak lewat backend).
const KEY = "ta.recentSearches";
const MAX = 5;

export function loadRecent() {
  try {
    const list = JSON.parse(localStorage.getItem(KEY) || "[]");
    return Array.isArray(list) ? list.filter((x) => typeof x === "string").slice(0, MAX) : [];
  } catch {
    return [];
  }
}

export function saveRecent(label) {
  if (!label) return;
  try {
    const next = [label, ...loadRecent().filter((x) => x !== label)].slice(0, MAX);
    localStorage.setItem(KEY, JSON.stringify(next));
  } catch {
    /* localStorage bisa tidak tersedia (mode private); abaikan */
  }
}
