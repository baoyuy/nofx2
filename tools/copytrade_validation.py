#!/usr/bin/env python3
"""Reproduce AI score outcome validation and time-split diagnostics."""
import argparse
import math
import sqlite3
import statistics
from datetime import datetime, timezone


HORIZONS = {'4h': ('ret_4h', 'bench_4h', 'done_4h'), '24h': ('ret_24h', 'bench_24h', 'done_24h'), '72h': ('ret_72h', 'bench_72h', 'done_72h')}


def dedup(rows, window_ms=4 * 3600 * 1000):
    """Match server's rolling dedup: reject calls near any kept call of same key."""
    kept, last = [], {}
    for r in sorted(rows, key=lambda x: x['analyzed_at']):
        key = (r['symbol'], r['bias'])
        if key in last and r['analyzed_at'] - last[key] < window_ms:
            continue
        last[key] = r['analyzed_at']
        kept.append(r)
    return kept


def band(row):
    score = max(row['bull'] or 0, row['bear'] or 0)
    return 'high' if score >= 70 else 'mid' if score >= 40 else 'low'

def wilson(hits, n, z=1.96):
    if not n:
        return (0, 0)
    p = hits / n
    den = 1 + z*z/n
    center = (p + z*z/(2*n)) / den
    radius = z * math.sqrt(p*(1-p)/n + z*z/(4*n*n)) / den
    return center-radius, center+radius


def stats(rows, ret_key, bench_key, bench=None):
    vals = [r[ret_key] for r in rows if r[ret_key] is not None]
    if not vals:
        return (0, 0, 0, 0, 0, 0)
    hits = sum(x > 0 for x in vals)
    beat = []
    excess = []
    for r in rows:
        if r[ret_key] is None:
            continue
        raw_bench = (bench or {}).get((r['analyzed_at'] // 1_800_000), 0)
        bm = -raw_bench if r['bias'] == 'short' else raw_bench
        beat.append(r[ret_key] > bm)
        excess.append(r[ret_key] - bm)
    return len(vals), hits / len(vals), sum(beat) / len(beat), statistics.mean(vals), statistics.mean(excess), statistics.median(vals)


def pct(x):
    return f'{100*x:.1f}%'


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('db')
    ap.add_argument('--out', default='-')
    args = ap.parse_args()
    conn = sqlite3.connect(f'file:{args.db}?mode=ro', uri=True)
    conn.row_factory = sqlite3.Row
    raw = conn.execute('''select symbol,bias,analyzed_at,bull,bear,ret_4h,ret_24h,ret_72h,bench_4h,bench_24h,bench_72h,done_4h,done_24h,done_72h
                          from copytrade_score_outcomes where user_id=(select user_id from copytrade_score_outcomes group by user_id order by count(*) desc limit 1)''').fetchall()
    report = ['# AI 币种评分验证', '', f'- 数据库：`{args.db}`', f'- 用户范围：主用户（{len(raw)} 条结果）', '- 口径：复用线上 `dedupOutcomes`（同币同方向，间隔不足 4 小时保留最早一条），先对全部已保存结果去重，再按各周期筛选成熟样本。方向命中使用已按预测方向正负化的收益，`ret > 0` 为命中。', '- 跑赢市场均值按线上页面口径计算：去重后的样本按 30 分钟 cohort 求原始多头市场收益均值；空头比较时翻转该基准。cohort 只使用当前成熟样本，与线上对所选返回集计算 benchmark 的方式一致。', '- 成本敏感性只做固定往返成本假设，不替代真实手续费、滑点和资金费率回放。', '']
    for horizon, (rk, bk, dk) in HORIZONS.items():
        unique = dedup(raw)
        sample = [r for r in unique if r[dk] and r[rk] is not None]
        report += [f'## {horizon}', '', '|分组|样本|方向命中|跑赢同 cohort 市场均值|平均方向收益|中位方向收益|', '|---|---:|---:|---:|---:|---:|']
        bench_raw = {}
        for r in sample:
            k = r['analyzed_at'] // 1_800_000
            v = -r[rk] if r['bias'] == 'short' else r[rk]
            a = bench_raw.setdefault(k, [0, 0]); a[0] += v; a[1] += 1
        bench = {k: s/n for k, (s,n) in bench_raw.items()}
        for label, subset in [('总体', sample), ('多头', [r for r in sample if r['bias']=='long']), ('空头', [r for r in sample if r['bias']=='short']), *[(f'{b}分', [r for r in sample if band(r)==b]) for b in ('high','mid','low')]]:
            n, hit, beat, avg, excess, med = stats(subset, rk, bk, bench)
            lo, hi = wilson(round(hit*n), n)
            report.append(f'|{label}|{n}|{pct(hit)} ({pct(lo)}–{pct(hi)})|{pct(beat)}|{pct(avg)}|{pct(med)}|')
        report.append('')
    # Chronological half split on 4h mature outcomes. This is diagnostic, not a robust OOS test.
    unique_4h = dedup(raw)
    matured = [r for r in unique_4h if r['done_4h'] and r['ret_4h'] is not None]
    bench_raw = {}
    for r in matured:
        k = r['analyzed_at'] // 1_800_000
        raw_return = -r['ret_4h'] if r['bias'] == 'short' else r['ret_4h']
        a = bench_raw.setdefault(k, [0, 0]); a[0] += raw_return; a[1] += 1
    split_bench = {k: s/n for k, (s,n) in bench_raw.items()}
    if matured:
        cut = sorted(r['analyzed_at'] for r in matured)[len(matured)//2]
        report += ['## 4 小时按时间切分（诊断）', '', f'- 切分时间：{datetime.fromtimestamp(cut/1000, timezone.utc).isoformat()}', '- 前后各约一半；当前数据覆盖仅数日，不能视为稳定的样本外证明。', '', '|区间|分组|样本|方向命中|平均方向收益|', '|---|---|---:|---:|---:|']
        for period, subset in [('前半段', [r for r in matured if r['analyzed_at'] < cut]), ('后半段', [r for r in matured if r['analyzed_at'] >= cut])]:
            for label, group in [('总体', subset), ('多头', [r for r in subset if r['bias']=='long']), ('空头', [r for r in subset if r['bias']=='short']), ('高分', [r for r in subset if band(r)=='high']), ('中分', [r for r in subset if band(r)=='mid']), ('低分', [r for r in subset if band(r)=='low'])]:
                n, hit, _, avg, _, _ = stats(group, 'ret_4h', 'bench_4h', split_bench)
                report.append(f'|{period}|{label}|{n}|{pct(hit)}|{pct(avg)}|')
    report += ['', '## 固定成本敏感性（4h）', '', '以方向调整后收益减去固定往返成本；不含动态资金费率。', '', '|分组|样本|成本 0.1% 后命中|成本 0.2% 后命中|成本 0.4% 后命中|0.2% 后平均净收益|', '|---|---:|---:|---:|---:|---:|']
    sample = [r for r in matured if r['bias'] in ('long', 'short')]
    for label, group in [('总体', sample), ('多头', [r for r in sample if r['bias']=='long']), ('空头', [r for r in sample if r['bias']=='short']), ('高分', [r for r in sample if band(r)=='high']), ('中分', [r for r in sample if band(r)=='mid'])]:
        vals = [r['ret_4h'] for r in group]
        n = len(vals)
        rates = [sum(v > c for v in vals) / n if n else 0 for c in (.001, .002, .004)]
        avg_net = (statistics.mean(vals)-.002) if n else 0
        report.append(f'|{label}|{n}|{pct(rates[0])}|{pct(rates[1])}|{pct(rates[2])}|{pct(avg_net)}|')
    text = '\n'.join(report) + '\n'
    if args.out == '-':
        print(text)
    else:
        with open(args.out, 'w', encoding='utf-8') as f:
            f.write(text)
        print(args.out)


if __name__ == '__main__':
    main()
