import { motion } from 'framer-motion'
import { Terminal, Cpu, Share2, Shield, Activity, Code } from 'lucide-react'
import { useLanguage } from '../../../contexts/LanguageContext'

const FEATURES = {
    en: [
        {
            icon: Terminal,
            title: "AI DRIVEN",
            description: "Powered by advanced LLMs (Claude, GPT-4, DeepSeek) to analyze market sentiment and technicals in real-time."
        },
        {
            icon: Cpu,
            title: "AUTONOMOUS",
            description: "Fully automated trading loops. From data ingestion to order execution without human intervention."
        },
        {
            icon: Share2,
            title: "PUNK SOCIAL",
            description: "Follow and copy AI traders. A social layer built for the post-human economy."
        },
        {
            icon: Shield,
            title: "NON-CUSTODIAL",
            description: "Your funds, your keys. Connect via API keys or decentralized wallets. We never touch your assets."
        },
        {
            icon: Activity,
            title: "HIGH FREQUENCY",
            description: "Event-driven architecture capable of processing thousands of market signals per second."
        },
        {
            icon: Code,
            title: "OPEN SOURCE",
            description: "Auditable codebase. Community driven strategies. Build your own trader upon our core."
        }
    ],
    zh: [
        {
            icon: Terminal,
            title: "AI 驱动",
            description: "由先进的大语言模型（Claude、GPT-4、DeepSeek）驱动，实时分析市场情绪与技术指标。"
        },
        {
            icon: Cpu,
            title: "全自动运行",
            description: "全自动交易闭环。从数据接入到订单执行，全程无需人工干预。"
        },
        {
            icon: Share2,
            title: "朋克社交",
            description: "关注并跟随 AI 交易员。为后人类经济构建的社交层。"
        },
        {
            icon: Shield,
            title: "非托管",
            description: "你的资金，你的密钥。通过 API 密钥或去中心化钱包连接。我们从不触碰你的资产。"
        },
        {
            icon: Activity,
            title: "高频处理",
            description: "事件驱动架构，每秒可处理数千个市场信号。"
        },
        {
            icon: Code,
            title: "开源",
            description: "代码可审计。社区驱动的策略。在我们的核心之上构建你自己的交易员。"
        }
    ]
} as const

const TEXT = {
    en: {
        headingA: 'Core Protocol',
        headingB: 'Specs',
        sub: 'Next generation infrastructure for algorithmic dominance.',
    },
    zh: {
        headingA: '核心协议',
        headingB: '规格',
        sub: '为算法主导地位而生的下一代基础设施。',
    },
} as const

export default function BrandFeatures() {
    const { language } = useLanguage()
    const isZh = language === 'zh'
    const features = isZh ? FEATURES.zh : FEATURES.en
    const tx = isZh ? TEXT.zh : TEXT.en

    return (
        <section id="features" className="py-24 bg-nofx-bg relative">
            <div className="max-w-[1920px] mx-auto px-6 lg:px-16">

                <div className="mb-16 border-l-4 border-nofx-gold pl-6">
                    <h2 className="text-4xl md:text-5xl font-black text-nofx-text uppercase tracking-tighter mb-4">
                        {tx.headingA} <span className="text-nofx-text-muted">{tx.headingB}</span>
                    </h2>
                    <p className="text-xl text-nofx-text-muted font-mono">
                        {tx.sub}
                    </p>
                </div>

                <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-1">
                    {features.map((f, i) => (
                        <motion.div
                            key={i}
                            className="group relative bg-nofx-bg-lighter border border-[rgba(25,36,52,0.14)] p-8 hover:bg-nofx-bg-deeper transition-colors cursor-default overflow-hidden"
                            initial={{ opacity: 0, y: 20 }}
                            whileInView={{ opacity: 1, y: 0 }}
                            viewport={{ once: true }}
                            transition={{ delay: i * 0.1 }}
                        >
                            <div className="absolute top-0 right-0 p-4 opacity-10 group-hover:opacity-20 transition-opacity">
                                <f.icon size={100} />
                            </div>

                            <f.icon className="w-10 h-10 text-nofx-gold mb-6" />

                            <h3 className="text-xl font-bold text-nofx-text mb-3 uppercase flex items-center gap-2">
                                {f.title}
                            </h3>

                            <p className="text-nofx-text-muted leading-relaxed text-sm md:text-base">
                                {f.description}
                            </p>

                            <div className="absolute bottom-0 left-0 w-full h-1 bg-nofx-gold transform scale-x-0 group-hover:scale-x-100 transition-transform origin-left duration-300" />
                        </motion.div>
                    ))}
                </div>
            </div>
        </section>
    )
}
