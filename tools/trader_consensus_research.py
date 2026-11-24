#!/usr/bin/env python3
"""Frozen-rules, both-side hourly consensus audit; no model or order calls."""
import argparse,bisect,json,math,statistics
from collections import defaultdict
from pathlib import Path
from urllib.parse import quote
from trader_direction_research import outcome,metrics,wilson

def summarize(rows):
 m=metrics(rows);n=len(rows)
 m['smooth']=sum(r['returns']['14400']>.002 and r['adverse_close_4h']<=.01 for r in rows)/n if n else 0
 m['coins']=len({r['symbol'] for r in rows});m['days']=len({r['at']//86400000 for r in rows})
 return m

def build(data,bars_dir):
 cache={}
 def bars(sym):
  if sym not in cache:
   p=Path(bars_dir)/(quote(sym,safe='')+'.json');b=json.load(open(p)) if p.exists() else []
   cache[sym]=([x['Time']+300 for x in b],[x['Close'] for x in b])
  return cache[sym]
 # Use all actions, including those whose later outcome is unavailable.
 # Otherwise a future-data coverage filter would change the apparent consensus.
 from trader_direction_research import classify
 actions=[]
 for trader in data['traders']:
  for e in classify(trader['events']):
   if e['category'] in ('new_open','trend_add','adverse_add'):
    actions.append(dict(e,id=trader['id']))
 actions.sort(key=lambda r:r['time'])
 cut=data['cut'];end=max((e['time'] for e in actions),default=cut)
 qualified={(x['id'],x['side'],x['action']) for x in data['qualified']}
 rows=[];index=0;active=[];last={}
 for at in range((cut//3600000+1)*3600000,end+1,3600000):
  while index<len(actions) and actions[index]['time']<=at:
   active.append(actions[index]);index+=1
  active=[e for e in active if e['time']>at-3600000]
  groups=defaultdict(dict)
  for e in active:groups[(e['symbol'],e['side'])][e['id']]=e
  for (sym,side),people in groups.items():
   if (sym,side) in last and at-last[(sym,side)]<14400000:continue
   # Two accounts executing within 60s are one timing cluster, not proof of
   # independence. Report raw accounts and conservative cluster count.
   ts=sorted(e['time'] for e in people.values());clusters=0;prev=None
   for t in ts:
    if prev is None or t-prev>60000:clusters+=1
    prev=t
   expert=sum((e['id'],side,e['category']) in qualified for e in people.values())
   opposition=len(groups.get((sym,'long' if side=='short' else 'short'),{}))
   b=bars(sym);times,prices=b;i=bisect.bisect_right(times,at//1000)-1
   if i<48 or times[i]!=at//1000:continue
   if times[i-48]!=times[i]-14400:continue
   sign=1 if side=='long' else -1
   momentum=sign*(prices[i]/prices[i-12]-1)>0 and sign*(prices[i]/prices[i-48]-1)>0
   o=outcome(dict(time=at,side=side),b)
   if not o:continue
   last[(sym,side)]=at
   rows.append(dict(o,symbol=sym,side=side,people=len(people),clusters=clusters,expert=expert,opposition=opposition,momentum=momentum))
 return rows

def main():
 p=argparse.ArgumentParser();p.add_argument('--events',required=True);p.add_argument('--evidence',required=True);p.add_argument('--bars',required=True);p.add_argument('--out',required=True);a=p.parse_args()
 data=json.load(open(a.events));evidence=json.load(open(a.evidence));data['qualified']=evidence['qualified']
 rows=build(data,a.bars)
 rules=[('全部近期动作',lambda r:True),('仅1个账户',lambda r:r['people']==1),('至少2个账户',lambda r:r['people']>=2),('至少2个时间簇',lambda r:r['clusters']>=2),('多账户+无反向账户',lambda r:r['people']>=2 and r['opposition']==0),('多账户+行情同向',lambda r:r['people']>=2 and r['momentum']),('训练选人有参与',lambda r:r['expert']>0),('训练选人+行情同向',lambda r:r['expert']>0 and r['momentum'])]
 lines=['# 多空对等：带单员共同动作验证','',
 '复用前段固定选人结果，后段每小时观察最近一小时的开仓/加仓；同币同方向观察至少间隔四小时。模拟在观察后至少五分钟入场。',
 '账户数量不等于独立信号：另用一分钟内同步动作合并的时间簇作敏感性检查，仍无法证明账户独立。',
 '这些规则在同一个后段对比，只是探索；下一轮需在未使用的数据验证，不能从本表挑最好的一行宣称有效。','',
 '|规则|方向|样本|币数/天数|方向命中|95%下界|平均净收益¹|盈利且反向≤1%²|',
 '|---|---|---:|---|---:|---:|---:|---:|']
 for name,rule in rules:
  for side in ('long','short'):
   m=summarize([r for r in rows if r['side']==side and rule(r)])
   lines.append(f'|{name}|{side}|{m["n"]}|{m["coins"]}/{m["days"]}|{m["hit"]:.1%}|{m["lower"]:.1%}|{m["mean_net"]:.2%}|{m["smooth"]:.1%}|')
 lines+=['','## 极端样本敏感性','',
  '|规则|方向|样本|净收益中位数|最差方向收益|去掉最佳一例后平均净收益|',
  '|---|---|---:|---:|---:|---:|']
 for name,rule in rules:
  if name not in ('全部近期动作','至少2个账户','训练选人有参与'):continue
  for side in ('long','short'):
   v=sorted(r['returns']['14400'] for r in rows if r['side']==side and rule(r))
   if v:lines.append(f'|{name}|{side}|{len(v)}|{statistics.median(v)-.002:.2%}|{min(v):.2%}|{statistics.mean(v[:-1])-.002 if len(v)>1 else 0:.2%}|')
 lines+=['','¹ 固定往返成本0.2%，不含动态滑点、资金费率。² 五分钟收盘采样，低估盘中反向波动。','',
 '## 适用范围','',
 '- 依赖历史可用缓存，不代表最新服务器行情。账户首次被观察前的历史开仓不算可靠新开。',
 '- 使用所有可回放动作生成共识，再检查后续行情；没有先按未来是否有结果筛选投票人。',
 '- 没有历史成交额、首次轮询可见时间、完整账户关联信息，不是最终1亿成交额候选池回测。',
 '- 各规则的观察币/时点不同，组间差异不构成因果证明；未作多重比较修正。',
 '- 尚未据此改变生产选币权重。']
 Path(a.out).write_text('\n'.join(lines)+'\n')
 print(a.out)
if __name__=='__main__':main()
