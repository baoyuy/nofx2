import {
  ArrowRight,
  ArrowUpRight,
  Layers3,
  SlidersHorizontal,
  Wallet,
  Check,
  Terminal,
} from 'lucide-react'
import { Link } from 'react-router-dom'
import { ROUTES } from '../../router/paths'
import { OFFICIAL_LINKS } from '../../constants/branding'
import { BrandLogo } from '../common/BrandLogo'
import { useLanguage } from '../../contexts/LanguageContext'

const TEXT = {
  en: {
    eyebrow: 'Open source. Self hosted.',
    heroTitleA: 'Intelligence',
    heroTitleB: 'that trades.',
    heroSub: 'An open trading system you run yourself.',
    openFrontier: 'Open Frontier',
    setupWorkspace: 'Set up your workspace',
    runsOnYourMachine: 'Runs on your machine',
    yourExchangeAccounts: 'Your exchange accounts',
    previewAria: 'Illustration of the Frontier planning workspace',
    workspacePreview: 'Workspace preview',
    oneIdea: 'One idea. A clear plan.',
    compareEntry: 'Compare your entry.',
    chartAria:
      'Illustrative chart showing an entry, target and stop level',
    target: 'Target',
    entry: 'Entry',
    stop: 'Stop',
    planTypes: ['Market', 'Limit', 'Ladder', 'TWAP'],
    planDescs: [
      'Enter now',
      'Choose a price',
      'Split by price',
      'Split by time',
    ],
    reviewBeforeTrade: 'Review before you trade',
    makeYourNextMove: 'Make your next move',
    exchangesAria: 'Supported Frontier exchanges',
    yourMarkets: 'Your markets. Connected.',
    featuresEyebrow: 'One connected workspace',
    featuresTitleA: 'Less friction.',
    featuresTitleB: 'More clarity.',
    featuresSub: 'AI helps you explore. Clear controls help you act.',
    features: [
      {
        title: 'See the alternatives.',
        body: 'Compare market, limit, ladder and TWAP plans. See entries, size and exits before committing.',
        label: 'Explore Frontier',
      },
      {
        title: 'Know where you stand.',
        body: 'Keep trading balances, open positions and orders in view. AI fees stay separate from trading funds.',
        label: 'Connect your accounts',
      },
      {
        title: 'Make it your own.',
        body: 'Choose your AI and trading accounts. Set up optional Autopilot strategies when you’re ready.',
        label: 'Build a strategy',
      },
    ],
    localEyebrow: 'Built to run locally',
    localTitleA: 'Your workspace.',
    localTitleB: 'On your terms.',
    localBody:
      'Install NOFX on your computer, connect your accounts and choose how you want to trade.',
    installationGuide: 'Installation guide',
  },
  zh: {
    eyebrow: '开源。自托管。',
    heroTitleA: '会交易的',
    heroTitleB: '智能系统。',
    heroSub: '一套由你自己运行的开放交易系统。',
    openFrontier: '打开 Frontier',
    setupWorkspace: '设置你的工作区',
    runsOnYourMachine: '运行在你的机器上',
    yourExchangeAccounts: '你自己的交易所账户',
    previewAria: 'Frontier 规划工作区示意图',
    workspacePreview: '工作区预览',
    oneIdea: '一个想法。一份清晰的计划。',
    compareEntry: '对比你的入场点。',
    chartAria: '示意图表：入场、目标与止损位',
    target: '目标',
    entry: '入场',
    stop: '止损',
    planTypes: ['市价', '限价', '阶梯', 'TWAP'],
    planDescs: ['立即入场', '指定价格', '按价格分批', '按时间分批'],
    reviewBeforeTrade: '交易前先复核',
    makeYourNextMove: '规划你的下一步',
    exchangesAria: 'Frontier 支持的交易所',
    yourMarkets: '你的市场，已连接。',
    featuresEyebrow: '一个互联的工作区',
    featuresTitleA: '更少摩擦。',
    featuresTitleB: '更清晰。',
    featuresSub: 'AI 帮你探索，清晰的控件帮你行动。',
    features: [
      {
        title: '看清每个备选方案。',
        body: '对比市价、限价、阶梯和 TWAP 计划。在投入前看清入场点、仓位大小和退出方式。',
        label: '探索 Frontier',
      },
      {
        title: '随时掌握你的状况。',
        body: '交易余额、未平仓位和订单一目了然。AI 费用与交易资金分开管理。',
        label: '连接你的账户',
      },
      {
        title: '打造成你的专属工具。',
        body: '选择你的 AI 和交易账户。准备好后，可设置可选的 Autopilot 自动策略。',
        label: '构建策略',
      },
    ],
    localEyebrow: '为本地运行而生',
    localTitleA: '你的工作区。',
    localTitleB: '按你的方式。',
    localBody: '把 NOFX 安装到你的电脑上，连接你的账户，按你想要的方式交易。',
    installationGuide: '安装指南',
  },
} as const

const FEATURE_ICONS = [Layers3, Wallet, SlidersHorizontal]
const FEATURE_ROUTES = [ROUTES.planning, ROUTES.traders, ROUTES.strategy]

export default function FlowLanding() {
  const { language } = useLanguage()
  const tx = language === 'zh' ? TEXT.zh : TEXT.en

  return (
    <div className="nofx-flow-landing">
      <section className="flow-hero">
        <div className="flow-hero-copy">
          <span className="flow-eyebrow">
            <span /> {tx.eyebrow}
          </span>
          <h1>
            {tx.heroTitleA}
            <br />
            <em>{tx.heroTitleB}</em>
          </h1>
          <p>{tx.heroSub}</p>
          <div className="flow-hero-actions">
            <Link to={ROUTES.planning} className="flow-primary">
              {tx.openFrontier} <ArrowRight size={18} />
            </Link>
            <Link to={ROUTES.traders}>
              {tx.setupWorkspace} <ArrowUpRight size={16} />
            </Link>
          </div>
          <div className="flow-hero-trust">
            <span>
              <Check size={14} /> {tx.runsOnYourMachine}
            </span>
            <span>
              <Check size={14} /> {tx.yourExchangeAccounts}
            </span>
          </div>
        </div>
        <div className="flow-product-preview" aria-label={tx.previewAria}>
          <div className="flow-preview-header">
            <BrandLogo />
            <span>Frontier</span>
            <small>{tx.workspacePreview}</small>
          </div>
          <div className="flow-preview-content">
            <div className="flow-preview-market">
              <span>{tx.oneIdea}</span>
              <b>{tx.compareEntry}</b>
            </div>
            <svg
              className="flow-preview-chart"
              viewBox="0 0 600 260"
              role="img"
              aria-label={tx.chartAria}
            >
              <defs>
                <linearGradient
                  id="flow-chart-fill"
                  x1="0"
                  y1="0"
                  x2="0"
                  y2="1"
                >
                  <stop stopColor="#295be8" stopOpacity=".13" />
                  <stop offset="1" stopColor="#295be8" stopOpacity="0" />
                </linearGradient>
              </defs>
              {[50, 100, 150, 200].map((y) => (
                <path key={y} d={`M0 ${y}H600`} stroke="#edf1f7" />
              ))}
              {[80, 180, 280, 380, 480].map((x) => (
                <path key={x} d={`M${x} 0V260`} stroke="#edf1f7" />
              ))}
              <path
                d="M0 214 30 200 52 210 80 185 105 190 133 156 154 169 182 128 203 137 229 98 250 119 274 108 300 145 326 129 352 142 378 100 398 115 425 77 449 94 471 59 494 81 520 66V260H0Z"
                fill="url(#flow-chart-fill)"
              />
              <path
                d="m0 214 30-14 22 10 28-25 25 5 28-34 21 13 28-41 21 9 26-39 21 21 24-11 26 37 26-16 26 13 26-42 20 15 27-38 24 17 22-35 23 22 26-15"
                fill="none"
                stroke="#295be8"
                strokeWidth="2.5"
                strokeLinejoin="round"
              />
              <rect x="350" y="48" width="174" height="87" fill="#eaf7f1" />
              <rect x="350" y="135" width="174" height="45" fill="#fdf0f0" />
              <path d="M350 48H600" stroke="#258563" strokeDasharray="4 4" />
              <text x="536" y="39" fill="#258563" fontSize="11">
                {tx.target}
              </text>
              <path d="M350 135H600" stroke="#295be8" />
              <text x="536" y="126" fill="#295be8" fontSize="11">
                {tx.entry}
              </text>
              <path d="M350 180H600" stroke="#ca565e" strokeDasharray="4 4" />
              <text x="536" y="198" fill="#ca565e" fontSize="11">
                {tx.stop}
              </text>
            </svg>
            <div className="flow-preview-plans">
              {tx.planTypes.map((type, i) => (
                <div key={type} data-selected={i === 1}>
                  <span>0{i + 1}</span>
                  <strong>{type}</strong>
                  <small>{tx.planDescs[i]}</small>
                </div>
              ))}
            </div>
            <div className="flow-preview-bottom">
              <span>
                <Check size={14} /> {tx.reviewBeforeTrade}
              </span>
              <span>
                {tx.makeYourNextMove} <ArrowUpRight size={14} />
              </span>
            </div>
          </div>
        </div>
      </section>
      <section className="flow-exchanges" aria-label={tx.exchangesAria}>
        <span>{tx.yourMarkets}</span>
        {['Binance', 'Hyperliquid', 'OKX', 'Bitget'].map((name) => (
          <b key={name}>{name}</b>
        ))}
      </section>
      <section className="flow-features">
        <div className="flow-section-heading">
          <span className="flow-eyebrow">{tx.featuresEyebrow}</span>
          <h2>
            {tx.featuresTitleA}
            <br />
            {tx.featuresTitleB}
          </h2>
          <p>{tx.featuresSub}</p>
        </div>
        {tx.features.map((item, i) => {
          const Icon = FEATURE_ICONS[i]
          return (
            <article key={item.title}>
              <Icon size={23} />
              <h3>{item.title}</h3>
              <p>{item.body}</p>
              <Link to={FEATURE_ROUTES[i]}>
                {item.label}
                <ArrowUpRight size={16} />
              </Link>
            </article>
          )
        })}
      </section>
      <section className="flow-local">
        <div>
          <span className="flow-eyebrow">
            <Terminal size={14} /> {tx.localEyebrow}
          </span>
          <h2>
            {tx.localTitleA}
            <br />
            {tx.localTitleB}
          </h2>
          <p>{tx.localBody}</p>
        </div>
        <a
          href={OFFICIAL_LINKS.github}
          target="_blank"
          rel="noopener noreferrer"
        >
          {tx.installationGuide} <ArrowUpRight size={18} />
        </a>
      </section>
    </div>
  )
}
