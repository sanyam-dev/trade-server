/** Shared helpers for the chart UI. */
window.TradeUI = window.TradeUI || {};

TradeUI.escapeHtml = (s) =>
  String(s)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");

TradeUI.fmtPrice = (n) => (n == null || Number.isNaN(n) ? "—" : Number(n).toFixed(2));

TradeUI.fmtVol = (n) => {
  if (n == null || Number.isNaN(n)) return "—";
  const v = Number(n);
  if (v >= 1e9) return (v / 1e9).toFixed(2) + "B";
  if (v >= 1e6) return (v / 1e6).toFixed(2) + "M";
  if (v >= 1e3) return (v / 1e3).toFixed(1) + "K";
  return String(v);
};

TradeUI.fetchJSON = async (url) => {
  const res = await fetch(url);
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
};

TradeUI.fetchBars = (symbol, interval) =>
  TradeUI.fetchJSON(
    `/api/bars?symbol=${encodeURIComponent(symbol)}&interval=${encodeURIComponent(interval)}`
  );

TradeUI.fetchNews = (date) =>
  TradeUI.fetchJSON(`/api/news?date=${encodeURIComponent(date)}`);
