#!/usr/bin/env python3
"""Read-only chronological diagnostic. Does not claim to replay absent snapshots."""
import argparse, sqlite3, math
from collections import defaultdict
from datetime import datetime, timezone

def lower(h,n):
    if not n:return 0
    z=1.96;p=h/n
    return (p+z*z/(2*n)-z*math.sqrt(p*(1-p)/n+z*z/(4*n*n)))/(1+z*z/n)

def independent(rows, key, timestamp, window):
    seen={}; out=[]
    for r in sorted(rows,key=lambda x:x[timestamp]):
        k=key(r);t=r[timestamp]
        if k in seen and t-seen[k]<window:continue
        seen[k]=t;out.append(r)
    return out

def audit(db):
    con=sqlite3.connect(f'file:{db}?mode=ro',uri=True);con.row_factory=sqlite3.Row
    user=con.execute('select user_id from copytrade_score_outcomes group by user_id order by count(*) desc limit 1').fetchone()[0]
    ai=independent(con.execute('select * from copytrade_score_outcomes where user_id=?',(user,)).fetchall(),lambda r:(r['symbol'],r['bias']),'analyzed_at',14400000)
    ai=[r for r in ai if r['bias'] in ('long','short') and r['done_4h'] and r['ret_4h'] is not None]
    sig=independent(con.execute('select * from copytrade_signals order by hour').fetchall(),lambda r:(r['symbol'],r['direction']),'hour',14400)
    sig=[r for r in sig if r['direction'] in ('long','short') and r['done_4h'] and r['ret_4h'] is not None]
    cut=sorted(r['analyzed_at'] for r in ai)[len(ai)//2]
    lines=['# 四小时方向选币：历史证据审计','',f'时间切分：{datetime.fromtimestamp(cut/1000,timezone.utc).isoformat()}','',
      '规则在本次审计前固定；前后段仅作诊断，不进行参数搜索。每币每方向至少间隔四小时，平盘保留为未命中，固定成本假设为往返 0.2%。',
      '数据来自已有服务器副本，范围截至该副本末尾；不代表当前服务器最新数据。','',
      '|来源|时段|方向|独立样本|方向命中|95%下界|扣0.2%后命中|平均净收益|',
      '|---|---|---|---:|---:|---:|---:|---:|']
    for source,rows,tkey,scale,signed in [('AI',ai,'analyzed_at',1,True),('带单持仓方向',sig,'hour',1000,False)]:
      for period in ('前段','后段'):
       for side in ('long','short'):
        vals=[r['ret_4h']*(1 if signed or side=='long' else -1) for r in rows if r['bias' if signed else 'direction']==side and ((r[tkey]*scale<cut) if period=='前段' else (r[tkey]*scale>=cut and (signed or r[tkey]*scale>=cut+14400000)))]
        n=len(vals);h=sum(x>0 for x in vals)
        lines.append(f'|{source}|{period}|{side}|{n}|{h/n:.1%}|{lower(h,n):.1%}|{sum(x>.002 for x in vals)/n:.1%}|{sum(x-.002 for x in vals)/n:.2%}|' if n else f'|{source}|{period}|{side}|0|—|—|—|—|')
    # Walk forward: calibrate a coin+side using only windows ending before now.
    selected=[];history=defaultdict(list)
    for row in ai:
      k=(row['symbol'],row['bias']);at=row['analyzed_at']
      past=[x for x in history[k] if x['analyzed_at']+14400000<=at]
      n=len(past);hits=sum(x['ret_4h']>0 for x in past)
      if at>=cut and n>=12 and lower(hits,n)>.5 and sum(x['ret_4h'] for x in past)/n>.002:selected.append(row)
      history[k].append(row)
    lines+=['','## 按时间向前验证历史门槛','',f'后段满足 AI 单来源历史门槛的预测：{len(selected)} 条。']
    for side in ('long','short'):
      v=[r['ret_4h'] for r in selected if r['bias']==side]
      lines.append(f'- {side}：{len(v)} 条'+(f'，命中 {sum(x>0 for x in v)/len(v):.1%}，成本后平均 {sum(x-.002 for x in v)/len(v):.2%}。' if v else '，证据不足。'))
    lines+=['','## 尚不能证明的事项','',
      '- 上述是已有信号审计，不是新方向榜的完整回测，也没有证明任意新权重有效。',
      '- 历史未完整保存每次筛选的成交额、当时风格信号及配置快照，不能用今天的成交额回填历史入选资格。',
      '- 现有结果主要是四小时端点收益，无法证明期间没有反弹；必须增加逐段K线回放与最大不利波动记录。',
      '- 当前行情动量基准、每次选币前十名及前向榜单快照尚未回放，因此不能断言优于简单趋势选币。',
      '- 历史结果的小时K线取价存在边界精度限制，应在完整回测中按时间和已闭合K线重算。',
      '- 研究榜未接入自动交易；严格门槛可能得到空榜，这是允许的结果。']
    return '\n'.join(lines)+'\n'
if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('db');p.add_argument('--out',required=True);a=p.parse_args()
    with open(a.out,'w') as f:f.write(audit(a.db))
