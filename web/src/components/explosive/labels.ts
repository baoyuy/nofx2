import { tr } from '../market/marketUi'
import { BN } from '../market/BinanceChart'
import type { ExplosiveState } from '../../lib/api/explosive'

export const stateLabel = (language: string, state: ExplosiveState) =>
  ({
    starting: tr(language, '刚启动', 'Starting'),
    accelerating: tr(language, '加速中', 'Accelerating'),
    overheated: tr(language, '过热', 'Overheated'),
    fading: tr(language, '衰减', 'Fading'),
    falling: tr(language, '回落', 'Falling'),
  })[state]

export const stateColor: Record<ExplosiveState, string> = {
  starting: BN.up,
  accelerating: '#b45309',
  overheated: BN.down,
  fading: '#94a3b8',
  falling: '#dc2626',
}

export const tagLabel = (language: string, tag: string) =>
  ({
    breakout: tr(language, '突破', 'Breakout'),
    volume: tr(language, '放量', 'Volume'),
    oi: tr(language, 'OI 增', 'OI up'),
    oi_drain: tr(language, 'OI 流出', 'OI draining'),
    aligned: tr(language, '多头排列', 'EMA uptrend'),
    hold_ema: tr(language, '站上 EMA20', 'Above EMA20'),
    funding_hot: tr(language, '费率过热', 'Hot funding'),
    chase: tr(language, '追高', 'Chasing'),
    fading: tr(language, '量能衰减', 'Fading'),
    off_high: tr(language, '离高点', 'Off high'),
    overheated: tr(language, '过热', 'Overheated'),
    falling: tr(language, '正在回落', 'Falling'),
  })[tag] || tag
