#!/usr/bin/env python3
"""Explosive-board / anomaly-scanner backtest report.

Two data sources, one report:

  # 1) Replay CSV written by `go run ./cmd/explosive_backtest`
  python3 scripts/explosive_analyze.py csv --csv /tmp/explosive_bt_all.csv \
        --xlsx data/explosive_backtest_report.xlsx

  # 2) The live recorder's own table (explosive_signals) straight from data.db
  python3 scripts/explosive_analyze.py db --db data/data.db \
        --xlsx data/explosive_recorded_report.xlsx

The CSV mode answers the whole-market question: among every liquid perp that
is up on the day, which early features raise the odds of a monster close, and
how many false positives does a rule throw off. The db mode scores the live
board's own recorded picks against their realised 1h/4h/24h returns.
"""
import argparse
import os
import sqlite3
import sys

import numpy as np
import pandas as pd


# ---------------------------------------------------------------- helpers
def pct(x, d=1):
    if x is None or (isinstance(x, float) and np.isnan(x)):
        return "—"
    return f"{x*100:+.{d}f}%"


def rate(series, thresh):
    s = series.dropna()
    if len(s) == 0:
        return np.nan
    return float((s >= thresh).mean())


def stat_line(df, retcol, ddcol=None):
    r = df[retcol].dropna()
    n = len(r)
    if n == 0:
        return dict(n=0)
    out = dict(
        n=n,
        mean=float(r.mean()),
        median=float(r.median()),
        up=float((r > 0).mean()),
        p20=rate(r, 0.20),
        p50=rate(r, 0.50),
        p100=rate(r, 1.00),
    )
    if ddcol and ddcol in df:
        dd = df[ddcol].dropna()
        out["dd15"] = float((dd <= -0.15).mean()) if len(dd) else np.nan
        out["avg_dd"] = float(dd.mean()) if len(dd) else np.nan
    return out


def add_common(row, label, s):
    row.append(dict(
        group=label, n=s["n"],
        mean=s.get("mean"), median=s.get("median"),
        up=s.get("up"), p20=s.get("p20"), p50=s.get("p50"), p100=s.get("p100"),
        dd15=s.get("dd15"), avg_dd=s.get("avg_dd"),
    ))


# ---------------------------------------------------------------- CSV mode
BUCKETS = {
    "volume spike (15m/7d med)": ("vol_spike", [0, 2, 5, 10, 20, 1e9],
                                  ["<2x", "2-5x", "5-10x", "10-20x", "≥20x"]),
    "ATR % (5m)": ("atr_pct", [0, 0.5, 1, 2, 1e9], ["<0.5%", "0.5-1%", "1-2%", "≥2%"]),
    "OI change 1h": ("oi_change_1h", [-1, -0.01, 0, 0.01, 0.03, 1e9],
                     ["drain < -1%", "-1~0%", "0-1%", "1-3%", "≥3%"]),
    "24h turnover (log10)": ("quote_vol", [0, 20e6, 50e6, 150e6, 1e9],
                             ["<20M", "20-50M", "50-150M", "≥150M"]),
    "7d breakout (px/prior high-1)": ("breakout_7d", [-1, 0, 1e9], ["below high", "broke out"]),
    "7d compression (max-min)/min": ("compress_7d", [0, 0.3, 0.6, 1.2, 1e9],
                                     ["<0.3 (tight)", "0.3-0.6", "0.6-1.2", "≥1.2 (loose)"]),
}


def csv_report(csv_path, xlsx_path):
    df = pd.read_csv(csv_path)
    df = df[df["remaining"].notna()].copy()
    # Final Beijing-day gain: (1+day_gain)*(1+remaining)-1
    df["final_gain"] = (1 + df["day_gain"]) * (1 + df["remaining"]) - 1
    df["early"] = (df["day_gain"] < 0.20) & (df["elapsed_h"] <= 10)

    rows_summary, rows_feat, rows_rule, rows_ic = [], [], [], []

    def block(d, label):
        add_common(rows_summary, label, stat_line(d, "final_gain", "max_dd"))

    block(df, "ALL rows (up on day)")
    block(df[df["early"]], "EARLY (day gain<20%, ≤10h)")
    block(df[(df["day_gain"] < 0.05) & (df["elapsed_h"] <= 6)], "IGNITION window (day<5%, ≤6h)")

    # Feature buckets, measured on the early subset (that is the ambush window).
    early = df[df["early"]].copy()
    for title, (col, edges, names) in BUCKETS.items():
        if col not in early:
            continue
        b = pd.cut(early[col], bins=edges, labels=names, include_lowest=True)
        for name, g in early.groupby(b, observed=False):
            s = stat_line(g, "final_gain", "max_dd")
            if s["n"] == 0:
                continue
            rows_feat.append(dict(feature=title, bucket=str(name), n=s["n"],
                                  med_final=s["median"], p20=s["p20"], p50=s["p50"],
                                  p100=s["p100"], dd15=s["dd15"]))

    # Named rules a user could actually run every few minutes.
    def apply_rule(mask, name):
        sel = early[mask]
        s = stat_line(sel, "final_gain", "max_dd")
        if s["n"] == 0:
            rows_rule.append(dict(rule=name, n=0)); return
        days = sel["day"].nunique()
        per_day = s["n"] / max(days, 1)
        hit_days = sel[sel["final_gain"] >= 0.50]["day"].nunique()
        rows_rule.append(dict(rule=name, n=s["n"], per_day=round(per_day, 1),
                              med_final=s["median"], p20=s["p20"], p50=s["p50"],
                              p100=s["p100"], dd15=s["dd15"],
                              hit_days=f"{hit_days}/{days}"))

    base = stat_line(early, "final_gain", "max_dd")
    rows_rule.append(dict(rule="BASE: all early movers", n=base["n"],
                          per_day=round(base["n"]/max(early['day'].nunique(),1), 1),
                          med_final=base["median"], p20=base["p20"], p50=base["p50"],
                          p100=base["p100"], dd15=base["dd15"],
                          hit_days=f"{early[early['final_gain']>=0.5]['day'].nunique()}/{early['day'].nunique()}"))
    oi_ok = (early["oi_known"] == True) | (early["oi_known"] == "True")
    apply_rule(early["vol_spike"] >= 2, "vol≥2x")
    apply_rule((early["vol_spike"] >= 2) & (early["atr_pct"] >= 1), "vol≥2x & ATR>1%")
    apply_rule((early["vol_spike"] >= 2) & (early["atr_pct"] >= 1) & (~oi_ok | (early["oi_change_1h"] > 0)),
               "vol≥2x & ATR>1% & OI>0")
    apply_rule((early["vol_spike"] >= 3) & (early["atr_pct"] >= 1) & (~oi_ok | (early["oi_change_1h"] > 0)),
               "vol≥3x & ATR>1% & OI>0")
    apply_rule(early["breakout_7d"] > 0, "broke 7d high")

    # Feature IC vs excess (removes same-hour drift), daily-averaged Spearman.
    feats = ["vol_spike", "atr_pct", "atr_7d_pct", "oi_change_1h", "breakout_7d",
             "compress_7d", "taker_ratio", "efficiency", "rel_strength",
             "day_gain", "vol_slope", "change_1h", "change_4h"]
    for f in feats:
        if f not in df:
            continue
        sub = df[[f, "excess", "day"]].dropna()
        if len(sub) < 50:
            continue
        # pooled spearman
        pooled = sub[f].rank().corr(sub["excess"].rank())
        # daily-averaged
        ics = []
        for _, g in sub.groupby("day"):
            if len(g) >= 8:
                ics.append(g[f].rank().corr(g["excess"].rank()))
        daily = float(np.nanmean(ics)) if ics else np.nan
        rows_ic.append(dict(feature=f, pooled=float(pooled) if pooled == pooled else np.nan,
                            daily_avg=daily, days=len(ics)))

    summary = pd.DataFrame(rows_summary)
    feat = pd.DataFrame(rows_feat)
    rule = pd.DataFrame(rows_rule)
    ic = pd.DataFrame(rows_ic).sort_values("daily_avg", key=lambda s: s.abs(), ascending=False) if len(rows_ic) else pd.DataFrame()
    _emit("WHOLE-MARKET REPLAY", summary, feat, rule, ic, df)
    _write_xlsx(xlsx_path, [(("Summary", summary)), ("ByFeature", feat),
                            ("Rules", rule), ("FeatureIC", ic)],
                title="Whole-market explosive replay")
    print(f"\n[ok] rows={len(df)} days={df['day'].nunique()} symbols={df['symbol'].nunique()}")


# ---------------------------------------------------------------- DB mode
def db_report(db_path, xlsx_path):
    con = sqlite3.connect(db_path)
    df = pd.read_sql_query("SELECT * FROM explosive_signals", con)
    df = df[df["done_24h"] == 1].copy()
    for c in ["ret_24h", "ret_4h", "ret_1h", "max_dd_24h", "max_dd_4h"]:
        if c in df:
            df[c] = pd.to_numeric(df[c], errors="coerce")

    rows_summary, rows_feat, rows_rule = [], [], []
    add_common(rows_summary, "all board rows", stat_line(df, "ret_24h", "max_dd_24h"))
    add_common(rows_summary, "rank top5", stat_line(df[df["rank"] <= 5], "ret_24h", "max_dd_24h"))
    add_common(rows_summary, "rank 6-20", stat_line(df[df["rank"] > 5], "ret_24h", "max_dd_24h"))

    df["score_band"] = pd.cut(df["score"], bins=[-1, 12, 22, 35, 1e9],
                              labels=["Q1 <12", "Q2 12-22", "Q3 22-35", "Q4 ≥35"])
    for name, g in df.groupby("score_band", observed=False):
        add_common(rows_summary, f"score {name}", stat_line(g, "ret_24h", "max_dd_24h"))
    for name, g in df.groupby("state"):
        add_common(rows_summary, f"state {name}", stat_line(g, "ret_24h", "max_dd_24h"))
    for name, g in df.groupby("radar"):
        add_common(rows_summary, f"radar={int(name)}", stat_line(g, "ret_24h", "max_dd_24h"))

    oi = pd.to_numeric(df["oi_change_1h"], errors="coerce")
    ign = (pd.to_numeric(df["vol_spike_1h"], errors="coerce") >= 2) & \
          (pd.to_numeric(df["atr_pct"], errors="coerce") >= 1) & (oi > 0)
    add_common(rows_summary, "ignition (vol≥2,ATR≥1,OI>0)", stat_line(df[ign], "ret_24h", "max_dd_24h"))
    add_common(rows_summary, "non-ignition", stat_line(df[~ign], "ret_24h", "max_dd_24h"))

    # score band table
    bandtbl = pd.DataFrame(rows_summary)
    _emit("RECORDED LIVE BOARD (24h realised)", bandtbl, pd.DataFrame(), pd.DataFrame(), pd.DataFrame(), df)

    # paper equity
    eq = pd.read_sql_query(
        "SELECT ts, equity, realized, unrealized, open_count FROM explosive_paper_equities ORDER BY ts", con)
    trades = pd.read_sql_query(
        "SELECT symbol, side, opened_at, entry_price, exit_price, realized_pnl, exit_reason, "
        "day_gain, vol_spike, oi_pct FROM explosive_paper_trades", con)
    sheets = [("Summary", bandtbl)]
    if len(eq):
        unit = "ms" if eq["ts"].abs().max() > 1e11 else "s"
        eq["time"] = pd.to_datetime(eq["ts"], unit=unit, utc=True).dt.tz_convert("Asia/Shanghai")
        sheets.append(("PaperEquity", eq[["time", "equity", "realized", "unrealized", "open_count"]]))
    if len(trades):
        sheets.append(("PaperTrades", trades))
    _write_xlsx(xlsx_path, sheets, title="Live-board recorded outcomes", equity=eq)
    if len(trades):
        w = (trades["realized_pnl"] > 0).mean()
        print(f"\npaper trades: n={len(trades)} win={w*100:.1f}% totalPnL={trades['realized_pnl'].sum():+.2f} "
              f"avg={trades['realized_pnl'].mean():+.3f}")
    if len(eq):
        print(f"paper equity: first={eq['equity'].iloc[0]:.2f} last={eq['equity'].iloc[-1]:.2f} "
              f"peak={eq['equity'].max():.2f} maxDD={((eq['equity']/eq['equity'].cummax())-1).min()*100:.1f}%")
    con.close()


# ---------------------------------------------------------------- output
def _emit(title, summary, feat, rule, ic, df):
    print("=" * 78)
    print(title)
    print("=" * 78)
    if len(summary):
        print("\n-- forward-return summary (return = to Beijing-day close / to horizon) --")
        print(summary.to_string(index=False, float_format=lambda v: f"{v*100:+.1f}%" if abs(v) < 5 else f"{v:.2f}"))
    if len(feat):
        print("\n-- by feature bucket (early window) --")
        print(feat.to_string(index=False))
    if len(rule):
        print("\n-- tradable rules (early window, final day gain) --")
        print(rule.to_string(index=False))
    if len(ic):
        print("\n-- feature IC vs same-hour excess (daily-averaged Spearman) --")
        print(ic.to_string(index=False))


def _write_xlsx(path, sheets, title="report", equity=None):
    if not path:
        return
    os.makedirs(os.path.dirname(os.path.abspath(path)), exist_ok=True)
    try:
        from openpyxl import Workbook
        from openpyxl.chart import LineChart, BarChart, Reference
        from openpyxl.styles import Font, PatternFill, Alignment
    except Exception as e:  # pragma: no cover
        print(f"[warn] openpyxl missing, skip xlsx: {e}")
        return
    def cellval(v):
        if v is None:
            return None
        if isinstance(v, (np.integer,)):
            return int(v)
        if isinstance(v, (np.floating,)):
            v = float(v)
        if isinstance(v, float) and np.isnan(v):
            return None
        if isinstance(v, (pd.Timestamp,)):
            return str(v)
        return v

    wb = Workbook()
    wb.remove(wb.active)
    for name, frame in sheets:
        if frame is None or len(frame) == 0:
            continue
        ws = wb.create_sheet(name[:31])
        for j, col in enumerate(frame.columns, 1):
            ws.cell(1, j, str(col))
        for i, row in enumerate(frame.itertuples(index=False), 2):
            for j, val in enumerate(row, 1):
                ws.cell(i, j, cellval(val))
        for cell in ws[1]:
            cell.font = Font(bold=True, color="FFFFFF")
            cell.fill = PatternFill("solid", fgColor="1F4E78")
            cell.alignment = Alignment(horizontal="center")
        for col in ws.columns:
            width = max(len(str(c.value)) if c.value is not None else 0 for c in col)
            ws.column_dimensions[col[0].column_letter].width = min(28, max(10, width + 2))
    # native charts
    try:
        if equity is not None and len(equity):
            ws = wb.create_sheet("EquityChart")
            ws["A1"] = "time"; ws["B1"] = "equity"
            for i, (t, v) in enumerate(zip(equity["time"], equity["equity"]), start=2):
                ws.cell(i, 1, str(t)); ws.cell(i, 2, float(v))
            ch = LineChart(); ch.title = "Paper equity"; ch.height = 9; ch.width = 20
            ch.y_axis.title = "USDT"; ch.x_axis.title = "time"
            data = Reference(ws, min_col=2, min_row=1, max_row=len(equity) + 1)
            ch.add_data(data, titles_from_data=True)
            ws.add_chart(ch, "D2")
    except Exception as e:  # pragma: no cover
        print(f"[warn] chart: {e}")
    wb.save(path)
    print(f"[ok] wrote {path}")


def main():
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="mode", required=True)
    c = sub.add_parser("csv"); c.add_argument("--csv", required=True); c.add_argument("--xlsx", default="")
    d = sub.add_parser("db"); d.add_argument("--db", required=True); d.add_argument("--xlsx", default="")
    a = ap.parse_args()
    if a.mode == "csv":
        csv_report(a.csv, a.xlsx)
    else:
        db_report(a.db, a.xlsx)


if __name__ == "__main__":
    main()
