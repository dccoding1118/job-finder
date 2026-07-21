window.JOBFINDER_MOCK = {
  activeState: "recommended",
  states: {
    recommended: {
      id: "job-001",
      source: "104",
      title: "雲端平台架構師",
      company: "企業雲端服務商",
      location: "台北市・混合辦公",
      salary: "待遇面議",
      updated: "剛剛擷取",
      verdict: "recommended",
      total: 84,
      reason: "雲端架構、平台治理與企業導入經驗高度吻合，職務也保留 AI 自動化的發揮空間。",
      dimensions: [
        ["技能", 88],
        ["領域", 82],
        ["資歷", 91],
        ["條件", 73],
        ["方向", 86]
      ],
      highlights: ["GCP", "平台治理", "雲地整合", "技術顧問"],
      description: "負責企業雲端平台的架構規劃、導入與治理，協助應用團隊建立標準化交付流程，並與資安及基礎設施團隊協作。",
      letterState: "none",
      applyState: "pending"
    },
    pending: {
      id: "job-002",
      source: "104",
      title: "資深雲端解決方案工程師",
      company: "軟體產品團隊",
      location: "新北市・可遠端",
      salary: "月薪區間未提供",
      updated: "評分中",
      verdict: "pending_score",
      reason: "條件篩選已通過，常駐 worker 正在進行五維評分。",
      dimensions: [],
      highlights: ["雲端平台", "解決方案", "技術協作"],
      description: "規劃雲端解決方案並協助產品團隊完成交付與維運流程。",
      letterState: "unavailable",
      applyState: "pending"
    },
    unfit: {
      id: "job-003",
      source: "104",
      title: "機器學習模型研究員",
      company: "資料科技團隊",
      location: "台北市・辦公室",
      salary: "待遇面議",
      updated: "1 分鐘前",
      verdict: "unfit",
      reason: "職務核心為模型訓練與論文研究，偏離目前鎖定的雲端架構與 AI 落地方向。",
      filterHits: ["必要關鍵字未命中", "職務方向不符"],
      dimensions: [],
      highlights: ["模型訓練", "研究", "Python"],
      description: "進行模型訓練、演算法研究與論文實驗。",
      letterState: "unavailable",
      applyState: "pending"
    },
    letterReady: {
      id: "job-004",
      source: "Yourator",
      title: "Cloud Platform Engineer",
      company: "金融科技團隊",
      location: "台北市・混合辦公",
      salary: "月薪 90k–120k",
      updated: "今天 09:42",
      verdict: "recommended",
      total: 81,
      reason: "企業雲端、微服務與交付自動化經驗吻合，技術深度與目標職級相符。",
      dimensions: [
        ["技能", 84],
        ["領域", 80],
        ["資歷", 87],
        ["條件", 76],
        ["方向", 79]
      ],
      highlights: ["GCP", "微服務", "CI/CD", "平台工程"],
      description: "建立雲端平台能力、服務交付標準與開發者自助流程。",
      letterState: "ready",
      letter: "您好：\n\n我具備企業雲端架構、微服務與交付流程自動化經驗，曾參與大型組織的雲端平台導入與治理。這些經驗與職缺強調的平台工程、跨團隊協作及持續交付高度契合。\n\n我期待進一步了解團隊目前的平台策略與工程挑戰。\n\n[你的姓名]\n[你的聯絡方式]",
      applyState: "pending"
    },
    offline: {
      id: "job-001",
      source: "104",
      title: "雲端平台架構師",
      company: "企業雲端服務商",
      location: "台北市・混合辦公",
      salary: "待遇面議",
      updated: "快取於今天 10:18",
      verdict: "recommended",
      total: 84,
      reason: "目前顯示最近一次快取結果；重新連線後才可更新狀態或產生求職信。",
      dimensions: [
        ["技能", 88],
        ["領域", 82],
        ["資歷", 91],
        ["條件", 73],
        ["方向", 86]
      ],
      highlights: ["GCP", "平台治理", "雲地整合", "技術顧問"],
      description: "負責企業雲端平台的架構規劃、導入與治理。",
      letterState: "none",
      applyState: "pending",
      offline: true
    }
  },
  queue: [
    { id: "queue-1", source: "104", title: "雲端解決方案顧問", company: "資訊服務團隊", location: "台北市", salary: "待遇面議" },
    { id: "queue-2", source: "104", title: "Java 雲原生資深工程師", company: "企業軟體團隊", location: "新北市", salary: "月薪區間未提供" },
    { id: "queue-3", source: "104", title: "平台可靠性工程師", company: "數位服務團隊", location: "台北市・可遠端", salary: "待遇面議" }
  ],
  shortlist: [
    { state: "recommended", score: 84, title: "雲端平台架構師", company: "企業雲端服務商", source: "104", letter: "尚未產生", apply: "待投遞" },
    { state: "letterReady", score: 81, title: "Cloud Platform Engineer", company: "金融科技團隊", source: "Yourator", letter: "信件就緒", apply: "待投遞" },
    { state: "recommended", score: 78, title: "資深後端平台工程師", company: "軟體產品團隊", source: "Yourator", letter: "尚未產生", apply: "待投遞" }
  ],
  runs: [
    { time: "今天 09:30", trigger: "排程抓取", fetched: 42, created: 8, error: "無" },
    { time: "昨天 18:12", trigger: "手動抓取", fetched: 37, created: 5, error: "無" }
  ],
  schedule: { time: "08:30", timezone: "台北時間" }
};
