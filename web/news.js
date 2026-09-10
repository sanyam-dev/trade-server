/** News panel: load headlines for a candle's as_of_date. */
window.TradeUI = window.TradeUI || {};

TradeUI.NewsPanel = class NewsPanel {
  constructor(els) {
    this.els = els;
    this.cache = Object.create(null);
    this.selected = null;
    this.token = 0;
  }

  clearCache() {
    this.cache = Object.create(null);
    this.selected = null;
  }

  select(date, { status } = {}) {
    if (!date) return;
    const d = String(date);
    if (d === this.selected && this.cache[d]) {
      this.render(d, this.cache[d]);
      return;
    }
    this.selected = d;
    if (status) status(`Selected ${d}`);
    this.load(d);
  }

  async load(date) {
    const { newsDate, newsCount, newsHint, newsList } = this.els;
    if (!newsList) return;

    if (this.cache[date]) {
      this.render(date, this.cache[date]);
      return;
    }

    const token = ++this.token;
    newsDate.textContent = date;
    newsCount.textContent = "…";
    newsHint.textContent = "Loading headlines…";
    newsList.innerHTML = `<div class="news-empty">Fetching market news…</div>`;

    try {
      const data = await TradeUI.fetchNews(date);
      if (token !== this.token) return;
      this.cache[date] = { count: data.count || 0, news: data.news || [] };
      this.render(date, this.cache[date]);
    } catch (err) {
      if (token !== this.token) return;
      newsCount.textContent = "err";
      newsHint.textContent = "Could not load news for this date.";
      newsList.innerHTML = `<div class="news-empty">${TradeUI.escapeHtml(err.message)}</div>`;
    }
  }

  render(date, payload) {
    const { newsDate, newsCount, newsHint, newsList } = this.els;
    const items = (payload && payload.news) || [];
    newsDate.textContent = date || "Select a candle";
    newsCount.textContent = payload ? `${payload.count} headlines` : "—";

    if (!items.length) {
      newsHint.textContent = "No headlines for this day in SQLite. Run: go run . update";
      newsList.innerHTML = `<div class="news-empty">No headlines for this session day.</div>`;
      return;
    }

    newsHint.textContent = "Market headlines for the selected candle date.";
    newsList.innerHTML = items
      .map((n) => {
        const href = n.url ? TradeUI.escapeHtml(n.url) : "#";
        const target = n.url ? ` target="_blank" rel="noopener noreferrer"` : "";
        const summary = n.summary
          ? `<p class="news-summary">${TradeUI.escapeHtml(n.summary)}</p>`
          : "";
        const t = n.datetime
          ? new Date(n.datetime * 1000).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })
          : "";
        return `<a class="news-item" role="listitem" href="${href}"${target}>
  <div class="news-meta">
    <span class="news-source">${TradeUI.escapeHtml(n.source || "news")}</span>
    <span>${TradeUI.escapeHtml(t)}</span>
  </div>
  <p class="news-headline">${TradeUI.escapeHtml(n.headline || "")}</p>
  ${summary}
</a>`;
      })
      .join("");
  }
};
