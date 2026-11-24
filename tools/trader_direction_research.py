#!/usr/bin/env python3
"""Trader-first, frozen-split research using recorded replay events and cached bars.
No network, AI calls, order writes or retrospective ROI-based trader selection.
"""
import argparse,bisect,json,math,statistics
from collections import defaultdict
from pathlib import Path
from urllib.parse import quote
from datetime import datetime,timezone,timedelta

HORIZONS=(3600,14400,86400)
COST=.002

def wilson(h,n):
    if not n:return 0
    z=1.96;p=h/n
    return (p+z*z/(2*n)-z*math.sqrt(p*(1-p)/n+z*z/(4*n*n)))/(1+z*z/n)

def metrics(rows,h=14400):
    vals=[r['returns'][str(h)] for r in rows if str(h) in r['returns']]
    n=len(vals)
    return {'n':n,'hit':sum(v>0 for v in vals)/n if n else 0,
      'net_hit':sum(v>COST for v in vals)/n if n else 0,
      'mean_net':statistics.mean(vals)-COST if n else 0,
      'lower':wilson(sum(v>0 for v in vals),n),
      'median_adverse':statistics.median([r['adverse_close_4h'] for r in rows]) if rows else 0}

def classify(events):
    """Label adds using only entry fills preceding that event, never today's PnL."""
    books={};out=[]
    for e in sorted(events,key=lambda e:e['time']):
      e=dict(e);k=(e['symbol'],e['side']);b=books.get(k)
      action=e['action'];cat=action
      if action=='open':
        # First observed open per coin/side may be an add to prehistory.
        cat='new_open' if b is not None and b['closed'] else 'first_observed_open'
        books[k]={'cost':e['price']*e['qty'],'qty':e['qty'],'closed':False}
      elif action=='add':
        if not b or b['closed'] or b['qty']<=0:cat='unknown_add'
        else:
          avg=b['cost']/b['qty'];under=(e['price']<avg if e['side']=='long' else e['price']>avg)
          cat='adverse_add' if under else 'trend_add'
          b['cost']+=e['price']*e['qty'];b['qty']+=e['qty']
      elif action in ('reduce','close') and b:
        if action=='close': b['closed']=True;b['qty']=0;b['cost']=0
        elif b['qty']>0:
          remain=max(0,b['qty']-e['qty']);b['cost']*=remain/b['qty'];b['qty']=remain
      e['category']=cat;out.append(e)
    return out

def outcome(e,bars):
    """Enter at first closed 5m candle >= event + 5m; includes execution delay.
    Endpoint requires exact interval alignment. Path is close-only, not OHLC MAE.
    """
    if not bars:return None
    times,prices=bars
    target=(e['time']+999)//1000+300
    idx=bisect.bisect_left(times,target)
    if idx>=len(times) or times[idx]-target>300:return None
    entry=prices[idx]
    if entry<=0:return None
    rets={};sign=1 if e['side']=='long' else -1
    for h in HORIZONS:
      j=bisect.bisect_left(times,times[idx]+h)
      if j>=len(times) or times[j]!=times[idx]+h:continue
      if any(times[k]-times[k-1]!=300 for k in range(idx+1,j+1)):continue
      rets[str(h)]=sign*(prices[j]/entry-1)
    if '14400' not in rets:return None
    j=idx+48;path=prices[idx:j+1]
    adverse=max(0,max(-sign*(p/entry-1) for p in path))
    return {'at':e['time'],'entry_at':times[idx]*1000,'returns':rets,'adverse_close_4h':adverse,
      'no_1pct_close_reversal':adverse<=.01}

def run(events_path,bars_dir,out_dir):
    raw=json.load(open(events_path));cut=raw['cut'];cache={};samples=[];profiles=[];coverage=defaultdict(int)
    for t in raw['traders']:
      events=classify(t['events']);seen={}
      # Dollar PnL explains observed fill cashflows only, not account return.
      pnl=defaultdict(float);gross_wins=[];close_events=0
      for e in events:
        pnl[e['symbol']]+=e['realized_pnl'];
        if e['realized_pnl']!=0:close_events+=1
        if e['realized_pnl']>0:gross_wins.append(e['realized_pnl'])
      total=sum(pnl.values());positive=sum(v for v in pnl.values() if v>0)
      profiles.append({'id':t['id'],'name':t['name'],'latest_roi':t['latest_roi'],
        'latest_mdd':t['latest_mdd'],'observed_realized':total,
        'observed_fees':sum(e['fee'] for e in events),
        'top_symbol':max(pnl,key=pnl.get) if pnl else '',
        'top_symbol_profit_share':max(pnl.values())/positive if positive else 0,
        'training_style':t['training_style'], 'rows':close_events})
      for e in events:
        if e['category'] not in ('new_open','first_observed_open','trend_add','adverse_add'):continue
        key=(e['symbol'],e['side'],e['category'])
        if key in seen and e['time']-seen[key]<14400000:continue
        seen[key]=e['time'];coverage['dedup_actions']+=1
        sym=e['symbol']
        if sym not in cache:
          p=Path(bars_dir)/(quote(sym,safe='')+'.json')
          data=json.load(open(p)) if p.exists() else []
          # Cache timestamps are candle OPEN times. Close available 5m later.
          cache[sym]=([b['Time']+300 for b in data],[b['Close'] for b in data])
        o=outcome(e,cache[sym])
        if o is None:coverage['no_complete_market_path']+=1;continue
        # Embargo training outcomes that extend beyond cutoff; test starts after cutoff.
        period='train' if o['entry_at']+86400000<=cut else ('test' if e['time']>=cut else 'embargo')
        samples.append(dict(o,id=t['id'],symbol=sym,side=e['side'],category=e['category'],
          style=t['training_style']['kind'],period=period))
    groups=defaultdict(list)
    for r in samples:groups[(r['id'],r['side'],r['category'],r['period'])].append(r)
    # Freeze trader/action selections on training only; test never affects eligibility.
    qualified={}
    for (trader,side,action,period),rows in groups.items():
      if period!='train' or action=='first_observed_open':continue
      m=metrics(rows)
      if m['n']>=12 and m['lower']>.5 and m['mean_net']>0:qualified[(trader,side,action)]=m
    selected=[r for r in samples if r['period']=='test' and (r['id'],r['side'],r['category']) in qualified]
    pct=lambda v:f'{v:.1%}'
    line=lambda label,rows: '|'+label+'|'+str(metrics(rows)['n'])+'|'+pct(metrics(rows)['hit'])+'|'+pct(metrics(rows)['net_hit'])+'|'+pct(metrics(rows)['mean_net'])+'|'+pct(metrics(rows)['median_adverse'])+'|'
    lines=['# 从带单员交易动作寻找四小时方向优势','',
      f'训练截止：{datetime.fromtimestamp(cut/1000,timezone.utc).astimezone(timezone(timedelta(hours=8))).isoformat()}。',
      f'读取 {len(profiles)} 位带单员；去重动作 {coverage["dedup_actions"]}；完整四小时行情样本 {len(samples)}；行情不足 {coverage["no_complete_market_path"]}。',
      '', '## 方法','',
      '- 复用项目真实持仓回放器，拆分再次新开仓、首次观察开仓、顺势加仓和逆势加仓。首次开仓可能来自历史缺口，不用于选人。',
      '- 加仓分类使用动作之前的成本；风格只用训练截止之前成交计算。每人/币/方向/动作至少间隔四小时，类别之间仍可能重叠。',
      '- 模拟入场延迟至少五分钟，使用之后第一个已闭合五分钟收盘价；不使用带单员的原始成交价作为跟随入场价。',
      '- 训练样本要求24小时窗口在切分前结束；测试严格在切分后。每组训练至少12例、方向命中Wilson下界大于50%、平均成本后收益大于0才入选。',
      '- 固定往返成本0.2%，不是实际滑点、手续费和资金费率。反向波动仅为五分钟收盘采样，低估真实盘中风险。',
      '- 月度ROI为当前资料字段，只用于解释性对照，绝不用于回头选择训练期的赢家。',
      '', '## 后段测试：动作是否有用','',
      '|方向/动作|样本|方向命中|扣成本后命中|平均净收益|反向波动中位数（收盘采样）|',
      '|---|---:|---:|---:|---:|---:|']
    for side in ('short','long'):
      for cat in ('new_open','trend_add','adverse_add','first_observed_open'):
        lines.append(line(side+'/'+cat,[r for r in samples if r['period']=='test' and r['side']==side and r['category']==cat]))
    lines+=['','## 只用前段表现选人，后段再验','',f'通过训练门槛的“带单员/方向/动作”组合：{len(qualified)}；对应测试样本：{len(selected)}。','',
      '|组别|样本|方向命中|扣成本后命中|平均净收益|反向波动中位数（收盘采样）|','|---|---:|---:|---:|---:|---:|']
    for side in ('short','long'):lines.append(line('前段选人/'+side,[r for r in selected if r['side']==side]))
    # Equal-time, equal-symbol pooled baseline limits regime bias. Still correlated.
    keys={(r['symbol'],r['side'],r['at']//14400000) for r in selected}
    baseline=[r for r in samples if r['period']=='test' and (r['symbol'],r['side'],r['at']//14400000) in keys and r['category']!='first_observed_open']
    lines.append(line('同期同币方向其他动作（相关基准）',baseline))
    lines+=['','## 当前月度ROI靠前账户：收益来源核对','',
      '这部分是事后描述，不能当预测排序。记录收益不等于完整月收益；缺少资金曲线、杠杆、资金费率和未实现亏损。','',
      '|账户|当前ROI%|训练期风格|已记录实现盈亏USDT|手续费字段合计|最大贡献币|该币占盈利币贡献|',
      '|---|---:|---|---:|---:|---|---:|']
    for p in sorted(profiles,key=lambda p:p['latest_roi'],reverse=True)[:15]:
      lines.append(f'|{p["name"].replace("|","/") or p["id"]}|{p["latest_roi"]:.1f}|{p["training_style"]["kind"]}|{p["observed_realized"]:.0f}|{p["observed_fees"]:.0f}|{p["top_symbol"]}|{p["top_symbol_profit_share"]:.1%}|')
    lines+=['','## 每位通过训练筛选者的后段结果','',
      '|账户ID|方向/动作|训练样本|训练命中|后段样本|后段命中|后段平均净收益|',
      '|---|---|---:|---:|---:|---:|---:|']
    for k,m in sorted(qualified.items(),key=lambda x:x[1]['lower'],reverse=True):
      test=metrics(groups[(*k,'test')]);lines.append(f'|{k[0]}|{k[1]}/{k[2]}|{m["n"]}|{m["hit"]:.1%}|{test["n"]}|{test["hit"]:.1%}|{test["mean_net"]:.2%}|')
    lines+=['','## 重点观察的带单员：动作与路径','',
      '|账户|动作|后段样本|1h命中|4h命中|24h命中|4h盈利且收盘反向≤1%|最差4h方向收益|',
      '|---|---|---:|---:|---:|---:|---:|---:|']
    names={p['id']:p['name'] for p in profiles}
    for k,m in qualified.items():
      rows=groups[(*k,'test')];n=len(rows)
      rates=[metrics(rows,h)['hit'] for h in HORIZONS]
      smooth=sum(r['returns']['14400']>COST and r['no_1pct_close_reversal'] for r in rows)/n if n else 0
      worst=min((r['returns']['14400'] for r in rows),default=0)
      lines.append(f'|{(names.get(k[0]) or k[0])}|{k[1]}/{k[2]}|{n}|{rates[0]:.1%}|{rates[1]:.1%}|{rates[2]:.1%}|{smooth:.1%}|{worst:.2%}|')
    # Compare each selected signal with the equally weighted cached universe at
    # exactly the same start/end. No historic listing/volume eligibility claimed.
    market=[];beat=[]
    for r in selected:
      at=r['entry_at']//1000;changes=[]
      for times,prices in cache.values():
        i=bisect.bisect_left(times,at);j=bisect.bisect_left(times,at+14400)
        if j<len(times) and i<len(times) and times[i]==at and times[j]==at+14400 and prices[i]>0:
          changes.append(prices[j]/prices[i]-1)
      if changes:
        b=statistics.mean(changes)*(1 if r['side']=='long' else -1)
        market.append((r['side'],b));beat.append((r['side'],r['returns']['14400']-b))
    lines+=['','## 同时段缓存市场基准','',
      '用相同入场/出场时刻的缓存币种等权价格变动比较；缓存宇宙存在选择偏差，包含非加密标的，不是可交易组合。','']
    for side in ('short','long'):
      b=[v for d,v in market if d==side];ex=[v for d,v in beat if d==side]
      if b:lines.append(f'- {side}：{len(b)}例，市场同方向平均收益 {statistics.mean(b):.2%}，选中信号平均超额 {statistics.mean(ex):.2%}，跑赢基准 {sum(x>0 for x in ex)/len(ex):.1%}。')
    lines+=['','## 重点组合的覆盖与同期对比','']
    for k in qualified:
      rows=groups[(*k,'test')]
      comps=[]
      for r in rows:
        at=r['entry_at']//1000;ch=[]
        for times,prices in cache.values():
          i=bisect.bisect_left(times,at);j=bisect.bisect_left(times,at+14400)
          if j<len(times) and i<len(times) and times[i]==at and times[j]==at+14400 and prices[i]>0:ch.append(prices[j]/prices[i]-1)
        if ch:comps.append(r['returns']['14400']-statistics.mean(ch)*(1 if r['side']=='long' else -1))
      days=len({r['at']//86400000 for r in rows});coins=len({r['symbol'] for r in rows})
      lines.append(f'- {(names.get(k[0]) or k[0])} {k[1]}/{k[2]}：测试仅覆盖 {days} 天、{coins} 个标的'+(f'，同期市场平均超额 {statistics.mean(comps):.2%}。' if comps else '。'))
    lines+=['','## 结论边界','', 
      '- 筛选151个人的多个动作会有多重比较偏差。后段是必要验证，仍需更长数据、独立时间段和同步市场方向基准。',
      '- 当前缓存止于较早时间，晚于缓存的交易未评估；不下载也不伪造行情补齐。',
      '- 没有历史轮询可见时间，五分钟延迟只是情景假设；真实可跟随效果仍需记录首次观察时刻。',
      '- 不能据此断言某个币必跌；要区分账户赚钱、动作后价格方向、可跟随收益三件事。',
      '- 此报告没有修改策略权重、生产排名或实盘配置。']
    out=Path(out_dir);out.mkdir(parents=True,exist_ok=True)
    (out/'trader-direction-research.md').write_text('\n'.join(lines)+'\n')
    (out/'trader-direction-evidence.json').write_text(json.dumps({'coverage':dict(coverage),'cut':cut,'profiles':profiles,'samples':samples,'selected_test_samples':selected,'qualified':[{'id':k[0],'side':k[1],'action':k[2],'train':m,'test':metrics(groups[(*k,'test')])} for k,m in qualified.items()]},ensure_ascii=False,indent=2))
    return len(samples),len(qualified),len(selected)
if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('--events',required=True);p.add_argument('--bars',required=True);p.add_argument('--out',required=True);a=p.parse_args();print(run(a.events,a.bars,a.out))
