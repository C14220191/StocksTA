"""Unit test parser output dan prompt builder (tanpa memanggil Hermes).
Jalankan dari folder ai-service:  python -m unittest test_app"""
import json
import unittest

import app

CANDLES = [{"ts": f"2026-11-04T0{i}:00:00Z", "open": 100, "high": 102, "low": 99, "close": 100 + i, "volume": 10}
           for i in range(3)]


def request(method, **kw):
    return app.AnalyzeRequest(symbol="BTCUSDT", method=method, candles=CANDLES, last_price=105.0,
                              last_price_ts="2026-11-04T03:10:00Z", **kw)


class LoadJSON(unittest.TestCase):
    def test_valid(self):
        data, repaired = app._load_json('teks {"bias": "bullish", "summary": "x"} teks')
        self.assertEqual(data["bias"], "bullish")
        self.assertFalse(repaired)

    def test_unclosed_array_before_key(self):
        # pola nyata dari upstage/solar-mini4 (4 Nov): array timeframes tidak ditutup
        raw = ('{"timeframes":[{"tf":"1D","note":"x"},{"tf":"1H","note":"berlawanan dengan 1D."},'
               '"confluence":{"score":2,"trend":false},"bias":"bearish","summary":"s"}')
        data, repaired = app._load_json(raw)
        self.assertTrue(repaired)
        self.assertEqual(len(data["timeframes"]), 2)
        self.assertEqual(data["confluence"]["score"], 2)
        self.assertEqual(data["bias"], "bearish")

    def test_close_arrays_ignores_colon_in_strings(self):
        s = '{"a":["x: y", "b"],"c":1}'
        self.assertEqual(app._close_arrays(s), s)

    def test_not_json(self):
        self.assertEqual(app._load_json('{:signal "neutral", :confidence 0}'), (None, False))
        self.assertEqual(app._load_json("tanpa json"), (None, False))


class ParseOutput(unittest.TestCase):
    def test_mtf_rules_enforced(self):
        raw = json.dumps({
            "timeframes": [{"tf": "1d", "trend": "Naik", "structure": "HH/HL", "aligned": False, "note": "n"},
                           {"tf": "4H", "trend": "naik", "aligned": "true"}, {"tf": "support", "note": "sampah"}],
            "confluence": {"score": 5, "trend": True, "structure": True, "level": False, "rsi": True, "volume": False},
            "setup": "jelas", "bias": "Bullish", "confidence": 140, "summary": "ringkas",
            "support": "100", "resistance": 110, "invalidation_level": 1e9})
        out = app.parse_output(raw, 105, "mtf")
        d = out["detail"]
        self.assertTrue(out["format_ok"])
        self.assertEqual(out["signal"], "BUY")
        self.assertEqual(out["confidence"], 100)
        self.assertEqual([t["tf"] for t in d["timeframes"]], ["1D", "4H"])
        self.assertTrue(d["timeframes"][0]["aligned"])           # 1D selalu acuan
        self.assertEqual(d["confluence"]["score"], 3)            # dihitung ulang dari faktor
        self.assertEqual(d["setup"], "tidak_jelas")              # score < 4
        self.assertIsNone(d["invalidation_level"])               # di luar rentang wajar
        self.assertEqual((out["support"], out["resistance"]), (100.0, 110.0))

    def test_levels_inverted_dropped(self):
        raw = json.dumps({"bias": "neutral", "summary": "s", "support": 110, "resistance": 100})
        out = app.parse_output(raw, 105, "grounded")
        self.assertEqual(out["signal"], "HOLD")
        self.assertIsNone(out["support"])
        self.assertIsNone(out["resistance"])

    def test_debate_missing_roles_filled(self):
        raw = json.dumps({"analysts": [{"role": "technical", "signal": "bearish", "confidence": 70, "reason": "r"}],
                          "debate": [{"bull": "a", "bear": "b"}], "risk": "turun tajam",
                          "bias": "bearish", "summary": "s"})
        d = app.parse_output(raw, 105, "debate")["detail"]
        self.assertEqual([a["role"] for a in d["analysts"]], ["fundamental", "technical", "sentiment"])
        self.assertEqual(d["analysts"][0]["signal"], "neutral")
        self.assertEqual(d["debate"][0]["round"], 1)
        self.assertEqual(d["risk"]["worst_case"], "turun tajam")

    def test_advice_terms_flagged(self):
        raw = json.dumps({"bias": "bullish", "summary": "Entry di 100 dengan stop loss 95."})
        d = app.parse_output(raw, 105, "grounded")["detail"]
        self.assertEqual(d["advice_terms"], ["entry", "stop loss"])

    def test_invalid_json_keeps_text(self):
        out = app.parse_output("maaf, tidak bisa", 105, "debate")
        self.assertFalse(out["format_ok"])
        self.assertIsNone(out["detail"])
        self.assertEqual(out["content"], "maaf, tidak bisa")


class Prompt(unittest.TestCase):
    def context(self, **kw):
        tf = lambda name, **v: app.TimeframeIn(tf=name, candles=CANDLES, n=250, **v)
        return app.ContextIn(name="Bitcoin", asset_type="crypto", timeframes=[
            tf("1D", ema50=100.0, ema200=90.0, rsi14=55.0, volume_avg20=10.0), tf("4H"), tf("1H")],
            stats=app.StatsIn(days=365, last_close=104.0, volatility_1h=0.004), **kw)

    def test_sections_per_method(self):
        ctx = self.context()
        debate = app.build_prompt(request("debate", context=ctx))
        mtf = app.build_prompt(request("mtf", context=ctx))
        grounded = app.build_prompt(request("grounded", context=ctx, indicators={"macd_hist": 1.0, "rsi14": 60.0}))
        self.assertIn("ta-bull-bear-debate", debate)
        for code in ("[D1]", "[D2]", "[D3]", "[D4]", "[D5]", "[D6]", "[D10]"):
            self.assertIn(code, debate)
        self.assertNotIn("[D7]", debate)
        for code in ("[D5]", "[D7]", "[D8]", "[D9]"):
            self.assertIn(code, mtf)
        self.assertNotIn("[D3]", mtf)
        self.assertIn("PANDANGAN DOMINAN", grounded)
        self.assertIn("BULLISH", grounded)  # 4 dari 4 pemeriksaan naik
        self.assertNotIn('"', debate + mtf + grounded)

    def test_dominant_view_mixed(self):
        ctx = self.context()
        req = request("grounded", context=ctx, indicators={"macd_hist": -1.0, "rsi14": 40.0})
        view = app.dominant_view(req)
        self.assertIn("NETRAL", view)

    def test_stock_fundamentals(self):
        f = {"currency": "IDR", "sector": "Financial Services", "business_summary": 'Bank "besar"',
             "valuation": {"per": 10.2, "pbv": 2.1, "der": 0.01, "market_cap": 6.05e14},
             "annual": [{"year": 2025, "revenue": 1.1432e14, "net_income": 5.754e13}]}
        ctx = self.context(fundamentals=f, fundamentals_at="2026-11-04T00:00:00Z")
        ctx.asset_type = "stock"
        p = app.build_prompt(request("debate", context=ctx))
        self.assertIn("PER 10.2", p)
        self.assertIn("605.00 triliun IDR", p)
        self.assertIn("2025: revenue 114.32 triliun", p)
        self.assertIn("Bank 'besar'", p)


if __name__ == "__main__":
    unittest.main()
