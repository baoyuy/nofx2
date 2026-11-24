#!/usr/bin/env python3
"""Fetch public candles and evaluate previously frozen trader/action selections.
Writes only local research files; never calls private exchange endpoints.
"""
import argparse,bisect,json,statistics,time,urllib.request,urllib.parse
from pathlib import Path
from trader_direction_research import classify,outcome,metrics

# Frozen before retrieving later prices. Existing cached data ended here.
START=1795015500000  # 2026-11-18; exclude overlapping old outcome windows

def context(bars,at,side):
 closed=[b for b in bars if b['Time']*1000+300000<=at]
 if len(closed)<49:return None
 b=closed[-49:]
 if any(y['Time']-x['Time']!=300 for x,y in zip(b,b[1:])):return None
 sign=1 if side=='long' else -1
 trend=sign*(b[-1]['Close']/b[-13]['Close']-1)>0 and sign*(b[-1]['Close']/b[0]['Close']-1)>0
 span=max(x['High'] for x in b[1:])/min(x['Low'] for x in b[1:])-1
 return {'trend':trend,'range4h':span,'volume24h':sum(x['QuoteVolume'] for x in closed[-288:]) if len(closed)>=288 and all(y['Time']-x['Time']==300 for x,y in zip(closed[-288:],closed[-287:])) else None}

def main():
 p=argparse.ArgumentParser();p.add_argument('--events',required=True);p.add_argument('--evidence',required=True);p.add_argument('--cache',required=True);p.add_argument('--out',required=True);a=p.parse_args()
 raw=json.load(open(a.events));ev=json.load(open(a.evidence))
 qualified={(x['id'],x['side'],x['action']) for x in ev['qualified']}
 # Only pre-selected combinations, no reselection on late outcomes.
 actions=[];names={};end=max(e['time'] for t in raw['traders'] for e in t['events'])
 for t in raw['traders']:
  names[t['id']]=t['name'] or t['id'];seen={}
  for e in classify(t['events']):
   key=(t['id'],e['side'],e['category'])
   if key not in qualified or e['time']<START or e['time']+14700000>end:continue
   k=(e['symbol'],e['side'],e['category'])
   if k in seen and e['time']-seen[k]<14400000:continue
   seen[k]=e['time'];actions.append(dict(e,id=t['id']))
 cache=Path(a.cache);cache.mkdir(parents=True,exist_ok=True);market={};errors={}
 for sym in sorted({e['symbol'] for e in actions}):
  path=cache/(urllib.parse.quote(sym,safe='')+'.json')
  try:
   if path.exists():b=json.load(open(path))
   else:
    b=[];cursor=START-86400000
    while cursor<end:
     query=urllib.parse.urlencode({'symbol':sym,'interval':'5m','startTime':cursor,'endTime':end,'limit':1500})
     with urllib.request.urlopen('https://fapi.binance.com/fapi/v1/klines?'+query,timeout=20) as r:rows=json.load(r)
     if not rows:break
     for x in rows:b.append({'Time':x[0]//1000,'Open':float(x[1]),'High':float(x[2]),'Low':float(x[3]),'Close':float(x[4]),'QuoteVolume':float(x[7])})
     nxt=rows[-1][0]+300000
     if nxt<=cursor:break
     cursor=nxt;time.sleep(.15)
    path.write_text(json.dumps(b))
   market[sym]=b
  except Exception as e:errors[sym]=str(e)
 samples=[];missing=0
 for e in actions:
  b=market.get(e['symbol'],[])
  o=outcome(e,([x['Time']+300 for x in b],[x['Close'] for x in b]))
  ctx=context(b,e['time'],e['side'])
  if not o or not ctx:missing+=1;continue
  path=[x for x in b if x['Time']*1000>=o['entry_at'] and (x['Time']+300)*1000<=o['entry_at']+14400000]
  entry=next(x['Close'] for x in b if (x['Time']+300)*1000==o['entry_at'])
  mae=max([0]+[(1-x['Low']/entry if e['side']=='long' else x['High']/entry-1) for x in path])
  samples.append(dict(o,**ctx,id=e['id'],symbol=e['symbol'],side=e['side'],category=e['category'],mae=mae))
 lines=['# 新时间段验证：带单员动作与行情条件','',
 '本轮先固定此前选出的四个“带单员/方向/动作”组合，再读取原缓存截止后公开行情。未根据新结果换人或修改门槛。',
 '使用2026-11-18原缓存之后至本地成交副本末尾的动作。新K线来自币安USDT-M公开接口，保存原始OHLC及成交额。',
 f'固定组合 {len(qualified)}；候选动作 {len(actions)}；有效样本 {len(samples)}；缺失/不完整 {missing}；行情下载错误 {len(errors)}。',
 '交易事件仍来自事后回填，首次可见时间未知；至少五分钟跟随延迟是情景假设，不是实盘成交模拟。','',
 '|账户/动作|方向|样本|方向命中|成本后均值|净收益中位数|盈利且盘中反向≤1%|最大反向波动|',
 '|---|---|---:|---:|---:|---:|---:|---:|']
 def line(label,side,rs):
  m=metrics(rs);n=len(rs)
  if not n:return f'|{label}|{side}|0|—|—|—|—|—|'
  v=[r['returns']['14400']-.002 for r in rs]
  return f'|{label}|{side}|{n}|{m["hit"]:.1%}|{m["mean_net"]:.2%}|{statistics.median(v) if v else 0:.2%}|{sum(r["returns"]["14400"]>.002 and r["mae"]<=.01 for r in rs)/n if n else 0:.1%}|{max((r["mae"] for r in rs),default=0):.2%}|'
 for ident,side,cat in sorted(qualified):lines.append(line(names[ident]+'/'+cat,side,[r for r in samples if (r['id'],r['side'],r['category'])==(ident,side,cat)]))
 lines+=['','## 预设行情条件的对照（不据此再挑最好结果）','',
 '|条件|方向|样本|方向命中|成本后均值|净收益中位数|盈利且盘中反向≤1%|最大反向波动|',
 '|---|---|---:|---:|---:|---:|---:|---:|']
 for label,rule in [('不加行情过滤',lambda r:True),('1h与4h行情同向',lambda r:r['trend']),('过去4h振幅≤3%',lambda r:r['range4h']<=.03),('24h成交额≥1亿',lambda r:r['volume24h'] is not None and r['volume24h']>=1e8),('同向且成交额≥1亿',lambda r:r['trend'] and r['volume24h'] is not None and r['volume24h']>=1e8)]:
  for side in ('long','short'):lines.append(line(label,side,[r for r in samples if r['side']==side and rule(r)]))
 lines+=['','## 限制','',
 '- 同一币多个账户动作、同一市场日的样本可能相关。样本少不能当稳定概率，不宣称新规则有效。',
 '- 该时段之前已用于探索的币种与账户存在选择偏差；本轮只把新增价格结果当后续验证，不称全项目从未接触过的盲测。',
 '- 部分原有标的在USDT-M接口不可用，失败会保留，不替换交易所或猜测价格。',
 '- 净收益固定扣0.2%，不含动态滑点、资金费率及实际止损。盘中MAE使用入场后5分钟OHLC，不等于逐笔精度。',
 '- 没有修改服务器、自动下单或生产权重。']
 Path(a.out).write_text('\n'.join(lines)+'\n')
 Path(a.out).with_suffix('.json').write_text(json.dumps({'samples':samples,'errors':errors,'start':START,'end':end},ensure_ascii=False,indent=2))
 print(a.out)
if __name__=='__main__':main()
