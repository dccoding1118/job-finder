// The list mark every semi-passive platform shares. The verdict wording and the
// visual weight belong here rather than to a platform script, so 104 and Cake
// cannot disagree about the same job. Loaded before the platform scripts.
globalThis.jobfinder = globalThis.jobfinder || {};

globalThis.jobfinder.VERDICTS = {
  unfit: { label: "不適合", background: "#fde8e8", color: "#8a1c1c", icon: "✕" },
  recommended: { label: "推薦", background: "#e6f6ea", color: "#0f5132", icon: "★" },
  not_recommended: { label: "不推薦", background: "#eceff1", color: "#5f6368", icon: "·" },
  pending_detail: { label: "待看", background: "#eef2ff", color: "#31407a", icon: "→" },
  pending_score: { label: "評分中", background: "#eef2ff", color: "#31407a", icon: "…" },
};

// mark renders one decision onto a list item. The mark lives in a Shadow DOM so
// the platform's own styles and this badge cannot pollute each other.
globalThis.jobfinder.mark = (node, decision) => {
  const verdict = globalThis.jobfinder.VERDICTS[decision.verdict];
  if (!verdict) return;
  let host = node.querySelector(":scope > .jobfinder-mark");
  if (!host) {
    host = document.createElement("div");
    host.className = "jobfinder-mark";
    host.attachShadow({ mode: "open" });
    node.prepend(host);
  }
  const detail = decision.filter_hits?.length
    ? `命中 ${decision.filter_hits.join("、")}`
    : decision.score_total != null
      ? `總分 ${decision.score_total}`
      : decision.verdict === "pending_detail"
        ? "點開內頁可取得完整評估"
        : "";
  // The mark never relies on color alone: the icon and the wording carry it.
  host.shadowRoot.innerHTML = `<style>
    .badge { display: inline-flex; gap: .4em; align-items: center; margin: .2em 0; padding: .15em .5em;
             border-radius: .4em; font: 600 12px/1.4 system-ui, sans-serif;
             background: ${verdict.background}; color: ${verdict.color}; }
  </style><p class="badge"><span aria-hidden="true">${verdict.icon}</span><span>jobfinder：${verdict.label}${detail ? `｜${detail}` : ""}</span></p>`;
  node.style.opacity = decision.verdict === "unfit" || decision.verdict === "not_recommended" ? "0.55" : "";
};
