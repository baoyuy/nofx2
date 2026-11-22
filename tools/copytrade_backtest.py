#!/usr/bin/env python3
"""Read-only local analysis for copy-trade style and 4h direction quality.

Usage: python tools/copytrade_backtest.py work/server-data.db --out work/copytrade-backtest.md
"""
import argparse, json, math, sqlite3, statistics
from collections import defaultdict
from datetime import datetime, timezone


def pct(v): return f"{v*100:.1f}%"
def smooth(hits, n, prior=8): return (hits + prior*0.5) / (n + prior) if n else 0.5

def style_for(events):
    if not events: return 'few_samples'
    events.sort()
    spans=[]; adds=0; adverse=0; opens=0
    for e in events:
        action, side, price, qty = e[1:]
        if action == 'add':
            adds += 1
            if side == 'short' and price > 0: adverse += 1
            elif side == 'long' and price > 0: adverse += 1
        if action == 'open': opens += 1
    # Fills do not always contain close linkage; activity itself remains useful.
    per_day=len(events)/max(1,(events[-1][0]-events[0][0])/86400000)
    if per_day >= 20: return 'high_freq'
    if adds >= max(3, opens): return 'martingale'
    if per_day >= 5: return 'scalp'
    if len(events) < 8: return 'few_samples'
    return 'swing'

def rows(conn, q, args=()): return conn.execute(q,args).fetchall()

def main():
    ap=argparse.ArgumentParser(); ap.add_argument('db'); ap.add_argument('--out', default='-'); args=ap.parse_args()
    con=sqlite3.connect(args.db); con.row_factory=sqlite3.Row
    now=int(datetime.now(timezone.utc).timestamp()*1000)
    fills=defaultdict(list)
    for r in rows(con, 'select portfolio_id,time,side,position_side,qty,price from copytrade_fills order by portfolio_id,time'):
        side=(r['position_side'] or r['side'] or '').lower()
        action='open' if side in ('long','short') else 'other'
        fills[r['portfolio_id']].append((r['time'], action, side, r['price'] or 0, r['qty'] or 0))
    styles={k:style_for(v) for k,v in fills.items()}
    sig=rows(con, '''select symbol,direction,ret_4h,bench_4h,done_4h,confidence,traders,hour
                     from copytrade_signals where done_4h=1 and ret_4h is not null''')
    # Directional hit: long wins on positive excess return, short on negative excess return.
    by=defaultdict(lambda:[0,0,0.0,0.0]); dedup={}
    for r in sig:
        key=(r['symbol'],r['direction'], round(r['hour']/14400000))
        if key in dedup: continue
        dedup[key]=1
        d=(r['direction'] or '').lower(); excess=(r['ret_4h'] or 0)-(r['bench_4h'] or 0)
        if d not in ('long','short'): continue
        hit=(excess>0) if d=='long' else (excess<0)
        g=by[d]; g[0]+=1; g[1]+=int(hit); g[2]+=excess; g[3]+=abs(excess)
    scores=rows(con, '''select symbol,bias,ret_4h,bench_4h,done_4h,bull,bear,analyzed_at
                        from copytrade_score_outcomes where done_4h=1 and ret_4h is not null''')
    ai=defaultdict(lambda:[0,0,0.0]); bands=defaultdict(lambda:[0,0]); style_ai=defaultdict(lambda:[0,0])
    for r in scores:
        b=(r['bias'] or '').lower(); ex=(r['ret_4h'] or 0)-(r['bench_4h'] or 0)
        if b not in ('long','short'): continue
        hit=(ex>0) if b=='long' else (ex<0)
        ai[b][0]+=1; ai[b][1]+=int(hit); ai[b][2]+=ex
        style_ai[(b, r['symbol'])][0]+=1; style_ai[(b, r['symbol'])][1]+=int(hit)
        edge=abs((r['bull'] or 0)-(r['bear'] or 0)); band=min(4,int(edge//10))
        bands[(b,band)][0]+=1; bands[(b,band)][1]+=int(hit)
    style_counts=defaultdict(lambda:[0,0])
    for s in styles.values(): style_counts[s][0]+=1
    lines=['# Copy-trade 本地回测报告','',f'- 生成时间：{datetime.now().astimezone().isoformat(timespec="seconds")}',f'- 数据库：{args.db}',f'- 4h 带单信号：{sum(x[0] for x in by.values())} 条（同币/方向/4h 去重）',f'- 4h AI 结果：{sum(x[0] for x in ai.values())} 条', '', '## 带单方向信号（4h）','', '|方向|样本|命中率|平均超额收益|校准命中率|','|---|---:|---:|---:|---:|']
    for d in ('long','short'):
        n,h,total,_=by[d]; lines.append(f'|{d}|{n}|{pct(h/n if n else 0)}|{pct(total/n if n else 0)}|{pct(smooth(h,n))}|')
    lines += ['', '## AI 4h 方向结果','', '|方向|样本|命中率|校准命中率|平均超额收益|','|---|---:|---:|---:|---:|']
    for d in ('long','short'):
        n,h,total=ai[d]; lines.append(f'|{d}|{n}|{pct(h/n if n else 0)}|{pct(smooth(h,n))}|{pct(total/n if n else 0)}|')
    lines += ['', '## 方向融合验证（按币种/方向分层）','', '|方向|成熟 AI 样本|经验命中率|Beta 平滑准确率|','|---|---:|---:|---:|']
    for d in ('long','short'):
        n,h,_=ai[d]; lines.append(f'|{d}|{n}|{pct(h/n if n else 0)}|{pct(smooth(h,n))}|')
    lines += ['', '## 带单员风格分布','', '|风格|带单员数|处理方式|','|---|---:|---|']
    desc={'high_freq':'保留，4h 评价并降权','martingale':'保留，4h 评价并作为风险提示','scalp':'4h 评价','swing':'24h 评价','long':'72h 评价','few_samples':'只展示，不参与方向投票'}
    for k in sorted(style_counts): lines.append(f'|{k}|{style_counts[k][0]}|{desc.get(k,"按自身周期评价")}|')
    lines += ['', '## 评分规则建议','', '稳定方向分 = 40% AI 4h 校准命中率 + 35% 带单 4h 校准命中率 + 15% 超额收益方向一致性 + 10% 样本新鲜度。每个方向至少 8 个成熟样本才进入主榜；否则标记为低样本。多空分别排名，避免总体偏空把样本少的多头隐藏。', '', '## 限制', '', '带单 fills 没有稳定的开仓/平仓配对时，风格识别只作为离线先验；异常价格、重复信号和未成熟结果必须继续过滤。命中率使用相对 benchmark 的超额方向，不能把单纯市场下跌误算成做空能力。']
    out='\n'.join(lines)+'\n'
    if args.out=='-': print(out)
    else:
        with open(args.out,'w',encoding='utf-8') as f:f.write(out)
        print(args.out)

if __name__=='__main__': main()
