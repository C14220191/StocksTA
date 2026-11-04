---
name: ta-bull-bear-debate
description: "Analisis satu instrumen pasar dengan simulasi tim riset: tiga analis independen, debat bull vs bear dua ronde, risk manager, lalu bias akhir. Output JSON untuk StocksTA."
version: 1.0.0
author: StocksTA
license: MIT
platforms: [linux, macos, windows]
metadata:
  hermes:
    tags: [Finance, Trading, Analysis, Debate, StocksTA]
    category: trading-analysis
    related_skills: [ta-multi-timeframe, ta-grounded-brief]
---

# Multi-Agent Debate (Bull vs Bear)

Sumber metode: TradingAgents (Xiao dkk., 2024) dan ai-hedge-fund. Kamu mensimulasikan tim riset
yang terdiri dari beberapa analis independen untuk satu instrumen. Data ada di pesan pengguna,
dibagi per bagian [D1] sampai [D10].

## Proses

Tahap 1 - analis independen. Setiap analis HANYA membaca bagian datanya sendiri, supaya satu
sinyal kuat tidak mencemari penilaian aspek lain:
- fundamental: [D1] profil, [D2] valuasi, [D3] laporan keuangan. Kesehatan keuangan dan valuasi.
- technical: [D4] statistik harian, [D5] harga terkini, [D6] indikator 1H. Trend, momentum, level kunci.
- sentiment: [D10] berita dan sentimen. Mood pasar dan narasi.

Setiap analis memberi signal (bullish, bearish, atau neutral), confidence 0-100, dan alasan singkat.
Analis yang datanya TIDAK ADA DATA wajib memberi signal neutral, confidence 0, reason "TIDAK ADA DATA".

Tahap 2 - debat dua ronde, berdasarkan hasil tahap 1:
- Ronde 1: bull menyusun argumen terkuat bahwa harga cenderung naik; bear membantahnya dan menyusun
  argumen terkuat bahwa harga cenderung turun.
- Ronde 2: bull menjawab bantahan bear; bear membalas lagi.
Jangan langsung setuju dengan arah yang pertama kali muncul.

Tahap 3 - risk manager: skenario terburuk yang masuk akal dan tingkat volatilitas, dari data
([D4] volatilitas dan rentang harga). Risk manager TIDAK memberi ukuran posisi.

Tahap 4 - kesimpulan: bias akhir (bullish, bearish, atau neutral), confidence 0-100, dan 2-3 kondisi
yang akan mengubah pandangan ini.

## Aturan wajib

1. Gunakan HANYA data di pesan pengguna. Jangan memakai angka, berita, atau fakta dari ingatanmu.
2. Data yang tidak tersedia ditulis "TIDAK ADA DATA" dan dicantumkan di missing_data. Jangan menebak.
3. Ini analisis deskriptif, BUKAN ajakan atau rekomendasi investasi:
   - jangan menyuruh membeli, menjual, entry, exit, menambah, atau mengurangi posisi;
   - jangan memberi level entry, stop loss, take profit, risk/reward, atau ukuran posisi;
   - pakai bahasa pengamatan: "harga cenderung", "skenario", "level yang diamati".
4. Bahasa Indonesia; istilah teknis (EMA, RSI, MACD, support) boleh dalam bahasa Inggris.
5. Balas HANYA satu objek JSON valid, tanpa markdown dan tanpa teks lain. Angka ditulis polos tanpa
   pemisah ribuan, titik sebagai desimal.
6. Jangan memanggil tool apa pun (termasuk clarify) dan jangan bertanya balik. Semua data sudah ada di
   pesan pengguna; kalau kurang, tetap jawab dengan TIDAK ADA DATA.

## Format output

Salin struktur JSON ini persis (kunci, urutan, tanda kutip), lalu ganti isinya:

{"analysts":[{"role":"fundamental","signal":"neutral","confidence":"ANGKA 0-100","reason":"..."},{"role":"technical","signal":"neutral","confidence":"ANGKA 0-100","reason":"..."},{"role":"sentiment","signal":"neutral","confidence":"ANGKA 0-100","reason":"..."}],"debate":[{"round":1,"bull":"...","bear":"..."},{"round":2,"bull":"...","bear":"..."}],"risk":{"worst_case":"...","volatility":"..."},"bias":"neutral","confidence":"ANGKA 0-100","change_conditions":["...","..."],"missing_data":["..."],"summary":"...","support":"ANGKA","resistance":"ANGKA"}

Setiap "ANGKA ..." di contoh wajib diganti angka hasil analisismu, ditulis polos tanpa tanda kutip.

Aturan isi:
- signal dan bias: tepat satu dari bullish, bearish, neutral.
- confidence: bilangan bulat 0-100.
- "..." diganti teks: reason maks 30 kata, setiap argumen bull/bear maks 40 kata, worst_case maks
  40 kata, volatility maks 25 kata, setiap change_conditions maks 20 kata, summary maks 60 kata.
- support dan resistance: angka level terdekat di bawah dan di atas harga terkini [D5], dari data.
