/**
 * trade-server market chart — Lightweight Charts playback UI + news dashboard.
 *
 * Bars: GET /api/bars → SQLite if present, else sample JSON.
 * News: GET /api/news?date=YYYY-MM-DD for the selected candle session day.
 * Live: connectLiveWS() when state.live === true; default remains REST playback.
 */
(() => {
  "use strict";

  const BASE_TICK_MS = 450;
  const WARMUP_BARS = 20;
  const FOLLOW_WINDOW = 48;
  const WS_BACKOFF_MIN_MS = 800;
  const WS_BACKOFF_MAX_MS = 20000;

  const state = {
    symbol: "SPY",
    interval: "daily",
    allBars: [],
    cursor: 0,
    playing: false,
    speed: 1,
    timer: null,
    source: "sample",
    /** When true, chart updates come from /ws instead of local playback. */
    live: false,
    chart: null,
    candleSeries: null,
    volumeSeries: null,
    tipPriceLine: null,
    /** @type {WebSocket|null} */
    ws: null,
    wsBackoffMs: WS_BACKOFF_MIN_MS,
    wsReconnectTimer: null,
    wsIdleTimer: null,
    wsGotFrame: false,
    intentionalClose: false,
    hovering: false,
    scrubbing: false,
    selectedNewsDate: null,
    /** @type {Record<string, {count:number, news:any[]}>} */
    newsCache: {},
    newsAbort: null,
    newsReqToken: 0,
  };

  const el = {
    chart: document.getElementById("chart"),
    play: document.getElementById("btnPlay"),
    pause: document.getElementById("btnPause"),
    back: document.getElementById("btnBack"),
    step: document.getElementById("btnStep"),
    reset: document.getElementById("btnReset"),
    status: document.getElementById("statusLine"),
    intervalBadge: document.getElementById("intervalBadge"),
    sourceBadge: document.getElementById("sourceBadge"),
    modeBadge: document.getElementById("modeBadge"),
    readout: document.getElementById("ohlcvReadout"),
    ohlcvOrigin: document.getElementById("ohlcvOrigin"),
    clockDate: document.getElementById("clockDate"),
    scrubber: document.getElementById("rangeScrubber"),
    scrubberPos: document.getElementById("scrubberPos"),
    scrubberTip: document.getElementById("scrubberTip"),
    newsDate: document.getElementById("newsDate"),
    newsCount: document.getElementById("newsCount"),
    newsHint: document.getElementById("newsHint"),
    newsList: document.getElementById("newsList"),
  };

  function fmtPrice(n) {
    if (n == null || Number.isNaN(n)) return "—";
    return Number(n).toFixed(2);
  }

  function fmtVol(n) {
    if (n == null || Number.isNaN(n)) return "—";
    const v = Number(n);
    if (v >= 1e9) return (v / 1e9).toFixed(2) + "B";
    if (v >= 1e6) return (v / 1e6).toFixed(2) + "M";
    if (v >= 1e3) return (v / 1e3).toFixed(1) + "K";
    return String(v);
  }

  function setStatus(msg) {
    el.status.textContent = msg;
  }

  function escapeHtml(s) {
    return String(s)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  function fmtNewsTime(unix) {
    if (!unix) return "";
    try {
      return new Date(unix * 1000).toLocaleTimeString([], {
        hour: "2-digit",
        minute: "2-digit",
      });
    } catch (_) {
      return "";
    }
  }

  function renderNewsEmpty(message) {
    if (!el.newsList) return;
    el.newsList.innerHTML = `<div class="news-empty">${escapeHtml(message)}</div>`;
  }

  function renderNewsList(date, payload) {
    if (!el.newsList) return;
    const items = (payload && payload.news) || [];
    el.newsDate.textContent = date || "Select a candle";
    el.newsCount.textContent = payload ? `${payload.count} headlines` : "—";

    if (!items.length) {
      el.newsHint.textContent =
        "No Finnhub headlines stored for this day. Recent ingest covers a short window — run go run . ingest-news.";
      renderNewsEmpty("No headlines for this session day.");
      return;
    }

    el.newsHint.textContent = "Shared US market feed (Finnhub general) for the selected candle date.";
    el.newsList.innerHTML = items
      .map((n) => {
        const href = n.url ? escapeHtml(n.url) : "#";
        const target = n.url ? ` target="_blank" rel="noopener noreferrer"` : "";
        const summary = n.summary
          ? `<p class="news-summary">${escapeHtml(n.summary)}</p>`
          : "";
        return `<a class="news-item" role="listitem" href="${href}"${target}>
  <div class="news-meta">
    <span class="news-source">${escapeHtml(n.source || "news")}</span>
    <span>${escapeHtml(fmtNewsTime(n.datetime))}</span>
  </div>
  <p class="news-headline">${escapeHtml(n.headline || "")}</p>
  ${summary}
</a>`;
      })
      .join("");
  }

  async function loadNewsForDate(date) {
    if (!date || !el.newsList) return;
    if (state.newsCache[date]) {
      renderNewsList(date, state.newsCache[date]);
      return;
    }

    const token = ++state.newsReqToken;
    el.newsDate.textContent = date;
    el.newsCount.textContent = "…";
    el.newsHint.textContent = "Loading headlines…";
    renderNewsEmpty("Fetching market news…");

    try {
      const res = await fetch(`/api/news?date=${encodeURIComponent(date)}`);
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = await res.json();
      if (token !== state.newsReqToken) return;
      state.newsCache[date] = { count: data.count || 0, news: data.news || [] };
      renderNewsList(date, state.newsCache[date]);
    } catch (err) {
      if (token !== state.newsReqToken) return;
      el.newsCount.textContent = "err";
      el.newsHint.textContent = "Could not load news for this date.";
      renderNewsEmpty(`Failed to load news: ${err.message}`);
    }
  }

  function selectNewsDate(date, reason) {
    if (!date) return;
    const d = String(date);
    if (d === state.selectedNewsDate && state.newsCache[d]) {
      return;
    }
    state.selectedNewsDate = d;
    if (reason === "click") {
      setStatus(`Selected ${d} — loading market news`);
    }
    loadNewsForDate(d);
  }

  function syncNewsFromTip() {
    const tip = tipBar();
    if (tip && tip.time) selectNewsDate(tip.time, "tip");
  }

  function tipBar() {
    if (state.cursor <= 0 || !state.allBars.length) return null;
    return state.allBars[Math.min(state.cursor, state.allBars.length) - 1];
  }

  function barAtCursorIndex(n) {
    if (!state.allBars.length) return null;
    if (n <= 0) return null;
    return state.allBars[Math.min(n, state.allBars.length) - 1];
  }

  function updateClock(bar) {
    el.clockDate.textContent = bar ? String(bar.time) : "—";
  }

  function setReadoutOrigin(origin) {
    if (!el.ohlcvOrigin) return;
    el.ohlcvOrigin.textContent = origin;
    el.ohlcvOrigin.dataset.origin = origin;
    el.ohlcvOrigin.title =
      origin === "hover" ? "OHLCV from crosshair hover" : "OHLCV from playback tip";
  }

  function scrubberDateLabel(n) {
    const tip = barAtCursorIndex(n);
    const first = state.allBars[0];
    const last = tip || first;
    if (!first) return "—";
    if (!tip) return `${first.time} · 0 / ${state.allBars.length}`;
    return `${first.time} → ${last.time} · ${n} / ${state.allBars.length}`;
  }

  function updateScrubberUI(n) {
    const tip = barAtCursorIndex(n);
    el.scrubberPos.textContent = scrubberDateLabel(n);
    el.scrubber.title = tip ? String(tip.time) : "";

    if (el.scrubberTip) {
      if (state.scrubbing && tip) {
        el.scrubberTip.hidden = false;
        el.scrubberTip.textContent = String(tip.time);
        const max = Number(el.scrubber.max) || 1;
        const pct = max > 0 ? (n / max) * 100 : 0;
        el.scrubberTip.style.left = `${pct}%`;
      } else {
        el.scrubberTip.hidden = true;
      }
    }
  }

  function updateControls() {
    const atEnd = state.cursor >= state.allBars.length;
    const empty = state.allBars.length === 0;
    const canPlay = !state.live && !empty && !state.playing && !atEnd;
    el.play.disabled = !canPlay;
    el.pause.disabled = state.live || !state.playing;
    el.step.disabled = state.live || empty || state.playing || atEnd;
    el.back.disabled = state.live || empty || state.playing || state.cursor <= 0;
    el.reset.disabled = state.live || empty;
    el.scrubber.disabled = state.live || empty || state.playing;

    el.sourceBadge.textContent = state.source;
    el.intervalBadge.textContent = state.interval.toUpperCase();
    el.modeBadge.textContent = state.live ? "live" : "playback";
    el.modeBadge.classList.toggle("is-live", state.live);

    if (!state.scrubbing && el.scrubber) {
      el.scrubber.max = String(state.allBars.length);
      el.scrubber.value = String(state.cursor);
    }
    updateScrubberUI(state.cursor);
  }

  function updateReadout(bar, origin) {
    const fields = {
      open: bar ? fmtPrice(bar.open) : "—",
      high: bar ? fmtPrice(bar.high) : "—",
      low: bar ? fmtPrice(bar.low) : "—",
      close: bar ? fmtPrice(bar.close) : "—",
      volume: bar ? fmtVol(bar.volume) : "—",
      time: bar ? String(bar.time) : "—",
    };
    for (const [key, val] of Object.entries(fields)) {
      const node = el.readout.querySelector(`[data-field="${key}"]`);
      if (node) node.textContent = val;
    }
    const up = bar && bar.close >= bar.open;
    el.readout.querySelectorAll(".ohlcv-item").forEach((item) => {
      item.classList.remove("is-up", "is-down");
      if (!bar || item.classList.contains("ohlcv-time")) return;
      item.classList.add(up ? "is-up" : "is-down");
    });
    if (origin) setReadoutOrigin(origin);
  }

  function syncReadoutFromTip() {
    const tip = tipBar();
    updateReadout(tip, "tip");
    updateClock(tip);
  }

  function toCandle(bar) {
    return {
      time: bar.time,
      open: bar.open,
      high: bar.high,
      low: bar.low,
      close: bar.close,
    };
  }

  function toVolume(bar) {
    return {
      time: bar.time,
      value: bar.volume,
      color: bar.close >= bar.open ? "rgba(38, 166, 154, 0.45)" : "rgba(239, 83, 80, 0.45)",
    };
  }

  function visibleSlice() {
    return state.allBars.slice(0, state.cursor);
  }

  /** @type {number|null} */
  let followRaf = null;

  /** Keep tip near the right edge so scrubber/playback stay aligned with the chart view. */
  function followTip(force) {
    if (!state.chart || state.cursor <= 0) return;
    // Idle pan: don't fight the user unless scrubbing / playing / live / forced.
    if (!force && !state.playing && !state.live && !state.scrubbing) return;

    const right = state.cursor - 0.5;
    const left = Math.max(-0.5, right - FOLLOW_WINDOW);
    try {
      state.chart.timeScale().setVisibleLogicalRange({ from: left, to: right + 3 });
    } catch (_) {
      try {
        state.chart.timeScale().scrollToRealTime();
      } catch (_) {
        /* ignore */
      }
    }
  }

  /**
   * Apply followTip after Lightweight Charts finishes setData layout.
   * A single sync call (or even one rAF) is often overwritten; double-rAF sticks.
   */
  function scheduleFollowTip(force) {
    if (!force && !state.playing && !state.live && !state.scrubbing) return;
    if (followRaf != null) cancelAnimationFrame(followRaf);
    followRaf = requestAnimationFrame(() => {
      followRaf = requestAnimationFrame(() => {
        followRaf = null;
        followTip(true);
      });
    });
  }

  function clearTipMarkers() {
    if (!state.candleSeries) return;
    state.candleSeries.setMarkers([]);
    if (state.tipPriceLine) {
      try {
        state.candleSeries.removePriceLine(state.tipPriceLine);
      } catch (_) {
        /* ignore */
      }
      state.tipPriceLine = null;
    }
  }

  function updateTipMarkers() {
    if (!state.candleSeries) return;
    const tip = tipBar();
    if (!tip) {
      clearTipMarkers();
      return;
    }

    const up = tip.close >= tip.open;
    const color = up ? "#26a69a" : "#ef5350";

    state.candleSeries.setMarkers([
      {
        time: tip.time,
        position: "aboveBar",
        color: "#c8ccd2",
        shape: "arrowDown",
        text: "now",
      },
    ]);

    if (state.tipPriceLine) {
      try {
        state.candleSeries.removePriceLine(state.tipPriceLine);
      } catch (_) {
        /* ignore */
      }
      state.tipPriceLine = null;
    }
    state.tipPriceLine = state.candleSeries.createPriceLine({
      price: tip.close,
      color,
      lineWidth: 1,
      lineStyle: LightweightCharts.LineStyle.Dashed,
      axisLabelVisible: true,
      title: "",
    });
  }

  function paintSeries() {
    const slice = visibleSlice();
    state.candleSeries.setData(slice.map(toCandle));
    state.volumeSeries.setData(slice.map(toVolume));
    updateTipMarkers();
    if (!state.hovering) syncReadoutFromTip();
    else updateClock(tipBar());
    updateControls();
  }

  function setCursor(n, opts = {}) {
    const max = state.allBars.length;
    const next = Math.max(0, Math.min(max, n | 0));
    if (next === state.cursor && !opts.force) return;
    state.cursor = next;
    paintSeries();
    if (opts.fit) {
      try {
        state.chart.timeScale().fitContent();
      } catch (_) {
        /* ignore */
      }
    } else {
      // Scrub / step / back: re-sync viewport to tip after setData settles.
      scheduleFollowTip(true);
    }
    syncNewsFromTip();
  }

  function advanceOne() {
    if (state.cursor >= state.allBars.length) {
      pause();
      setStatus("Playback complete — Reset to replay");
      return false;
    }
    const bar = state.allBars[state.cursor];
    state.cursor += 1;
    state.candleSeries.update(toCandle(bar));
    state.volumeSeries.update(toVolume(bar));
    updateTipMarkers();
    // Playing → follow; idle Step also forces via scheduleFollowTip(true) in step().
    scheduleFollowTip(false);
    if (!state.hovering) {
      updateReadout(bar, "tip");
    }
    updateClock(bar);
    updateControls();
    selectNewsDate(bar.time, "tip");
    return true;
  }

  function stepBack() {
    if (state.playing || state.live || state.cursor <= 0) return;
    setCursor(state.cursor - 1);
    setStatus("Stepped back one bar");
  }

  function clearTimer() {
    if (state.timer != null) {
      clearTimeout(state.timer);
      state.timer = null;
    }
  }

  function scheduleNext() {
    clearTimer();
    if (!state.playing || state.live) return;
    const delay = BASE_TICK_MS / state.speed;
    state.timer = setTimeout(() => {
      if (!advanceOne()) return;
      scheduleNext();
    }, delay);
  }

  function play() {
    if (state.live || state.playing || state.cursor >= state.allBars.length) return;
    state.playing = true;
    el.play.classList.add("is-active");
    setStatus(`Playing @ ${state.speed}x`);
    updateControls();
    scheduleFollowTip(true);
    scheduleNext();
  }

  function pause() {
    state.playing = false;
    el.play.classList.remove("is-active");
    clearTimer();
    updateControls();
    if (!state.live && state.cursor < state.allBars.length && state.allBars.length) {
      setStatus("Paused");
    }
  }

  function step() {
    if (state.playing || state.live) return;
    if (advanceOne()) {
      scheduleFollowTip(true);
      setStatus("Stepped one bar");
    }
  }

  function reset() {
    if (state.live) return;
    pause();
    const warm = Math.min(WARMUP_BARS, state.allBars.length);
    setCursor(warm, { force: true, fit: true });
    setStatus(`Warmup ${warm} bars — Play to continue the session`);
  }

  function findBarByTime(time) {
    if (time == null) return null;
    const t = String(time);
    for (let i = 0; i < state.cursor; i++) {
      if (String(state.allBars[i].time) === t) return state.allBars[i];
    }
    for (let i = 0; i < state.allBars.length; i++) {
      if (String(state.allBars[i].time) === t) return state.allBars[i];
    }
    return null;
  }

  function onCrosshairMove(param) {
    if (!param || !param.time) {
      state.hovering = false;
      syncReadoutFromTip();
      return;
    }
    const bar = findBarByTime(param.time);
    if (!bar) {
      state.hovering = false;
      syncReadoutFromTip();
      return;
    }
    state.hovering = true;
    updateReadout(bar, "hover");
    updateClock(tipBar());
  }

  function onChartClick(param) {
    if (!param || !param.time) return;
    const bar = findBarByTime(param.time);
    if (!bar) return;
    state.hovering = false;
    updateReadout(bar, "tip");
    updateClock(bar);
    selectNewsDate(bar.time, "click");
  }

  function initChart() {
    if (typeof LightweightCharts === "undefined") {
      setStatus("Failed to load Lightweight Charts CDN");
      return;
    }

    state.chart = LightweightCharts.createChart(el.chart, {
      layout: {
        background: { type: "solid", color: "#0b0d10" },
        textColor: "#8b939e",
        fontFamily: "'IBM Plex Sans', sans-serif",
        fontSize: 11,
      },
      grid: {
        vertLines: { color: "rgba(255,255,255,0.07)" },
        horzLines: { color: "rgba(255,255,255,0.07)" },
      },
      crosshair: {
        mode: LightweightCharts.CrosshairMode.Normal,
        vertLine: { color: "#4a5560", width: 1, style: LightweightCharts.LineStyle.Dashed },
        horzLine: { color: "#4a5560", width: 1, style: LightweightCharts.LineStyle.Dashed },
      },
      rightPriceScale: {
        borderColor: "#2a3038",
        scaleMargins: { top: 0.08, bottom: 0.22 },
      },
      timeScale: {
        borderColor: "#2a3038",
        timeVisible: false,
        rightOffset: 4,
        barSpacing: 8,
      },
      handleScroll: { vertTouchDrag: false },
    });

    state.candleSeries = state.chart.addCandlestickSeries({
      upColor: "#26a69a",
      downColor: "#ef5350",
      borderUpColor: "#26a69a",
      borderDownColor: "#ef5350",
      wickUpColor: "#26a69a",
      wickDownColor: "#ef5350",
    });

    state.volumeSeries = state.chart.addHistogramSeries({
      priceFormat: { type: "volume" },
      priceScaleId: "vol",
    });
    state.chart.priceScale("vol").applyOptions({
      scaleMargins: { top: 0.82, bottom: 0 },
      borderVisible: false,
    });

    state.chart.subscribeCrosshairMove(onCrosshairMove);
    state.chart.subscribeClick(onChartClick);

    const ro = new ResizeObserver(() => {
      if (!state.chart) return;
      const { width, height } = el.chart.getBoundingClientRect();
      state.chart.applyOptions({ width, height });
    });
    ro.observe(el.chart);
  }

  function normalizeBars(payload) {
    const raw = Array.isArray(payload) ? payload : payload.bars || [];
    return raw.map((b) => ({
      time: b.time || b.date,
      open: Number(b.open),
      high: Number(b.high),
      low: Number(b.low),
      close: Number(b.close),
      volume: Number(b.volume) || 0,
    }));
  }

  async function loadBars(symbol, interval) {
    pause();
    setStatus(`Loading ${symbol} ${interval}…`);
    const url = `/api/bars?symbol=${encodeURIComponent(symbol)}&interval=${encodeURIComponent(interval)}`;
    const res = await fetch(url);
    if (!res.ok) {
      throw new Error(`HTTP ${res.status}`);
    }
    const data = await res.json();
    state.symbol = (data.symbol || symbol).toUpperCase();
    state.interval = data.interval || interval;
    state.source = data.source || "sample";
    state.allBars = normalizeBars(data);
    state.hovering = false;
    state.selectedNewsDate = null;
    state.newsCache = {};

    const warm = Math.min(WARMUP_BARS, state.allBars.length);
    setCursor(warm, { force: true, fit: true });

    if (state.allBars.length) {
      setStatus(`Warmup ${warm}/${state.allBars.length} bars (${state.source}) — click a candle for news`);
      sendSubscribe();
    } else {
      clearTipMarkers();
      syncReadoutFromTip();
      updateControls();
      setStatus("No bars available");
      renderNewsEmpty("Load bars to select a candle.");
    }
  }

  function applyLiveBar(msg) {
    if (!msg || msg.time == null) return;
    if (msg.symbol && String(msg.symbol).toUpperCase() !== state.symbol) return;

    const bar = {
      time: msg.time,
      open: Number(msg.open),
      high: Number(msg.high),
      low: Number(msg.low),
      close: Number(msg.close),
      volume: Number(msg.volume) || 0,
    };

    const idx = state.allBars.findIndex((b) => String(b.time) === String(bar.time));
    if (idx >= 0) {
      state.allBars[idx] = bar;
    } else {
      state.allBars.push(bar);
    }
    state.cursor = state.allBars.length;
    state.source = "ws";
    state.wsGotFrame = true;
    clearWsIdleTimer();

    state.candleSeries.update(toCandle(bar));
    state.volumeSeries.update(toVolume(bar));
    updateTipMarkers();
    scheduleFollowTip(true);
    if (!state.hovering) updateReadout(bar, "tip");
    updateClock(bar);
    updateControls();
    setStatus(`Live bar ${bar.time}`);
  }

  function sendSubscribe() {
    if (!state.ws || state.ws.readyState !== WebSocket.OPEN) return;
    try {
      state.ws.send(JSON.stringify({ type: "subscribe", symbol: state.symbol }));
    } catch (_) {
      /* ignore — stub server may not read */
    }
  }

  function clearWsIdleTimer() {
    if (state.wsIdleTimer != null) {
      clearTimeout(state.wsIdleTimer);
      state.wsIdleTimer = null;
    }
  }

  function armWsIdleStatus() {
    clearWsIdleTimer();
    if (!state.live) return;
    state.wsIdleTimer = setTimeout(() => {
      if (state.live && state.ws && state.ws.readyState === WebSocket.OPEN && !state.wsGotFrame) {
        setStatus(`WS connected · subscribed ${state.symbol} · idle (no frames yet)`);
      }
    }, 2500);
  }

  function scheduleWsReconnect() {
    if (!state.live || state.intentionalClose) return;
    if (state.wsReconnectTimer != null) return;
    const delay = state.wsBackoffMs;
    setStatus(`WS disconnected — reconnecting in ${(delay / 1000).toFixed(1)}s…`);
    state.wsReconnectTimer = setTimeout(() => {
      state.wsReconnectTimer = null;
      connectLiveWS({ reconnect: true });
    }, delay);
    state.wsBackoffMs = Math.min(WS_BACKOFF_MAX_MS, Math.round(state.wsBackoffMs * 1.8));
  }

  /**
   * WebSocket binding with exponential reconnect.
   * Default mode is REST playback (live=false). Use ?live=1 to consume frames.
   * On open, sends { type:"subscribe", symbol }.
   */
  function connectLiveWS(opts = {}) {
    const params = new URLSearchParams(location.search);
    if (params.get("live") === "1" || params.get("live") === "true") {
      state.live = true;
    }

    if (state.ws) {
      state.intentionalClose = true;
      try {
        state.ws.close();
      } catch (_) {
        /* ignore */
      }
      state.ws = null;
      state.intentionalClose = false;
    }

    if (state.wsReconnectTimer != null && !opts.reconnect) {
      clearTimeout(state.wsReconnectTimer);
      state.wsReconnectTimer = null;
    }

    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const url = `${proto}//${location.host}/ws`;

    let ws;
    try {
      ws = new WebSocket(url);
    } catch (err) {
      if (state.live) {
        setStatus(`WS connect failed: ${err.message}`);
        scheduleWsReconnect();
      }
      updateControls();
      return null;
    }

    state.ws = ws;
    state.wsGotFrame = false;

    ws.onopen = () => {
      state.wsBackoffMs = WS_BACKOFF_MIN_MS;
      sendSubscribe();
      if (state.live) {
        pause();
        setStatus(`WS connected · subscribed ${state.symbol} · waiting for bars…`);
        armWsIdleStatus();
      }
      updateControls();
    };

    ws.onmessage = (ev) => {
      let msg;
      try {
        msg = JSON.parse(ev.data);
      } catch (_) {
        return;
      }
      if (!state.live) return;
      const type = (msg.type || "bar").toLowerCase();
      if (type === "subscribed" || type === "ack" || type === "ping") {
        if (!state.wsGotFrame) {
          setStatus(`WS connected · subscribed ${state.symbol} · idle (no frames yet)`);
        }
        return;
      }
      if (type !== "bar" && type !== "ohlcv" && type !== "candle") return;
      applyLiveBar(msg);
    };

    ws.onerror = () => {
      if (state.live) setStatus("WS error");
    };

    ws.onclose = () => {
      state.ws = null;
      clearWsIdleTimer();
      if (state.intentionalClose) return;
      if (state.live) {
        scheduleWsReconnect();
      }
      updateControls();
    };

    return ws;
  }

  function bindUI() {
    el.play.addEventListener("click", play);
    el.pause.addEventListener("click", pause);
    el.step.addEventListener("click", step);
    el.back.addEventListener("click", stepBack);
    el.reset.addEventListener("click", reset);

    el.scrubber.addEventListener("pointerdown", () => {
      state.scrubbing = true;
      pause();
    });
    el.scrubber.addEventListener("input", () => {
      const n = Number(el.scrubber.value) || 0;
      setCursor(n);
      updateScrubberUI(n);
      // Force another follow in case setCursor early-returned (same value).
      scheduleFollowTip(true);
    });
    el.scrubber.addEventListener("change", () => {
      state.scrubbing = false;
      const n = Number(el.scrubber.value) || 0;
      setCursor(n, { force: true });
      updateScrubberUI(n);
      scheduleFollowTip(true);
      const tip = barAtCursorIndex(n);
      setStatus(tip ? `Scrubbed to ${tip.time}` : `Scrubbed to ${n}`);
    });
    el.scrubber.addEventListener("pointerup", () => {
      state.scrubbing = false;
      updateControls();
      scheduleFollowTip(true);
    });
    el.scrubber.addEventListener("pointercancel", () => {
      state.scrubbing = false;
      updateControls();
      scheduleFollowTip(true);
    });

    document.querySelectorAll(".speed-btn").forEach((btn) => {
      btn.addEventListener("click", () => {
        document.querySelectorAll(".speed-btn").forEach((b) => b.classList.remove("is-active"));
        btn.classList.add("is-active");
        state.speed = Number(btn.dataset.speed) || 1;
        if (state.playing) {
          setStatus(`Playing @ ${state.speed}x`);
          scheduleNext();
        }
      });
    });

    document.querySelectorAll(".symbol-btn").forEach((btn) => {
      btn.addEventListener("click", async () => {
        const sym = btn.dataset.symbol;
        if (!sym || sym === state.symbol) return;
        document.querySelectorAll(".symbol-btn").forEach((b) => {
          const on = b.dataset.symbol === sym;
          b.classList.toggle("is-active", on);
          b.setAttribute("aria-pressed", on ? "true" : "false");
        });
        try {
          await loadBars(sym, state.interval);
        } catch (err) {
          setStatus(`Failed to load ${sym}: ${err.message}`);
        }
      });
    });

    window.addEventListener("keydown", (e) => {
      if (e.target && /input|textarea|select/i.test(e.target.tagName)) return;
      if (e.code === "Space") {
        e.preventDefault();
        if (state.playing) pause();
        else play();
      } else if (e.code === "ArrowRight") {
        e.preventDefault();
        step();
      } else if (e.code === "ArrowLeft") {
        e.preventDefault();
        stepBack();
      } else if (e.code === "KeyR") {
        reset();
      }
    });
  }

  async function boot() {
    initChart();
    bindUI();
    connectLiveWS();
    try {
      await loadBars(state.symbol, state.interval);
    } catch (err) {
      setStatus(`Failed to load bars: ${err.message}`);
      updateControls();
    }
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", boot);
  } else {
    boot();
  }
})();
