/**
 * Chart playback UI — study desk for bars + news.
 * ML / RL / policy live elsewhere; wire actions via TradeUI.onAction(action, ctx).
 */
(() => {
  "use strict";

  const BASE_TICK_MS = 450;
  const WARMUP = 20;
  const FOLLOW = 48;

  const state = {
    symbol: "SPY",
    interval: "daily",
    allBars: [],
    cursor: 0,
    playing: false,
    speed: 1,
    timer: null,
    source: "sample",
    chart: null,
    candle: null,
    volume: null,
    tipLine: null,
    hovering: false,
    scrubbing: false,
    followRaf: null,
  };

  const $ = (id) => document.getElementById(id);
  const el = {
    chart: $("chart"),
    play: $("btnPlay"),
    pause: $("btnPause"),
    back: $("btnBack"),
    step: $("btnStep"),
    reset: $("btnReset"),
    status: $("statusLine"),
    intervalBadge: $("intervalBadge"),
    sourceBadge: $("sourceBadge"),
    readout: $("ohlcvReadout"),
    ohlcvOrigin: $("ohlcvOrigin"),
    clockDate: $("clockDate"),
    scrubber: $("rangeScrubber"),
    scrubberPos: $("scrubberPos"),
    scrubberTip: $("scrubberTip"),
  };

  const news = new TradeUI.NewsPanel({
    newsDate: $("newsDate"),
    newsCount: $("newsCount"),
    newsHint: $("newsHint"),
    newsList: $("newsList"),
  });

  const tipBar = () =>
    state.cursor > 0 && state.allBars.length
      ? state.allBars[Math.min(state.cursor, state.allBars.length) - 1]
      : null;

  const barAt = (n) =>
    n > 0 && state.allBars.length
      ? state.allBars[Math.min(n, state.allBars.length) - 1]
      : null;

  const setStatus = (msg) => {
    el.status.textContent = msg;
  };

  function setOrigin(origin) {
    if (!el.ohlcvOrigin) return;
    el.ohlcvOrigin.textContent = origin;
    el.ohlcvOrigin.dataset.origin = origin;
  }

  function updateReadout(bar, origin) {
    const fields = {
      open: TradeUI.fmtPrice(bar && bar.open),
      high: TradeUI.fmtPrice(bar && bar.high),
      low: TradeUI.fmtPrice(bar && bar.low),
      close: TradeUI.fmtPrice(bar && bar.close),
      volume: TradeUI.fmtVol(bar && bar.volume),
      time: bar ? String(bar.time) : "—",
    };
    for (const [k, v] of Object.entries(fields)) {
      const node = el.readout.querySelector(`[data-field="${k}"]`);
      if (node) node.textContent = v;
    }
    const up = bar && bar.close >= bar.open;
    el.readout.querySelectorAll(".ohlcv-item").forEach((item) => {
      item.classList.remove("is-up", "is-down");
      if (!bar || item.classList.contains("ohlcv-time")) return;
      item.classList.add(up ? "is-up" : "is-down");
    });
    if (origin) setOrigin(origin);
    el.clockDate.textContent = bar ? String(bar.time) : "—";
  }

  function updateControls() {
    const empty = !state.allBars.length;
    const atEnd = state.cursor >= state.allBars.length;
    el.play.disabled = empty || state.playing || atEnd;
    el.pause.disabled = !state.playing;
    el.step.disabled = empty || state.playing || atEnd;
    el.back.disabled = empty || state.playing || state.cursor <= 0;
    el.reset.disabled = empty;
    el.scrubber.disabled = empty || state.playing;
    el.sourceBadge.textContent = state.source;
    el.intervalBadge.textContent = state.interval.toUpperCase();

    if (!state.scrubbing) {
      el.scrubber.max = String(state.allBars.length);
      el.scrubber.value = String(state.cursor);
    }
    const tip = barAt(state.cursor);
    const first = state.allBars[0];
    el.scrubberPos.textContent = !first
      ? "—"
      : !tip
        ? `${first.time} · 0 / ${state.allBars.length}`
        : `${first.time} → ${tip.time} · ${state.cursor} / ${state.allBars.length}`;
    if (el.scrubberTip) {
      if (state.scrubbing && tip) {
        el.scrubberTip.hidden = false;
        el.scrubberTip.textContent = String(tip.time);
        const max = Number(el.scrubber.max) || 1;
        el.scrubberTip.style.left = `${max ? (state.cursor / max) * 100 : 0}%`;
      } else {
        el.scrubberTip.hidden = true;
      }
    }
  }

  function toCandle(b) {
    return { time: b.time, open: b.open, high: b.high, low: b.low, close: b.close };
  }

  function toVolume(b) {
    return {
      time: b.time,
      value: b.volume,
      color: b.close >= b.open ? "rgba(38,166,154,0.45)" : "rgba(239,83,80,0.45)",
    };
  }

  function followTip() {
    if (!state.chart || state.cursor <= 0) return;
    const right = state.cursor - 0.5;
    const left = Math.max(-0.5, right - FOLLOW);
    try {
      state.chart.timeScale().setVisibleLogicalRange({ from: left, to: right + 3 });
    } catch (_) {
      /* ignore */
    }
  }

  function scheduleFollow() {
    if (state.followRaf != null) cancelAnimationFrame(state.followRaf);
    state.followRaf = requestAnimationFrame(() => {
      state.followRaf = requestAnimationFrame(() => {
        state.followRaf = null;
        followTip();
      });
    });
  }

  function updateTipMarker() {
    if (!state.candle) return;
    const tip = tipBar();
    if (!tip) {
      state.candle.setMarkers([]);
      if (state.tipLine) {
        try {
          state.candle.removePriceLine(state.tipLine);
        } catch (_) {
          /* ignore */
        }
        state.tipLine = null;
      }
      return;
    }
    const color = tip.close >= tip.open ? "#26a69a" : "#ef5350";
    state.candle.setMarkers([
      { time: tip.time, position: "aboveBar", color: "#c8ccd2", shape: "arrowDown", text: "now" },
    ]);
    if (state.tipLine) {
      try {
        state.candle.removePriceLine(state.tipLine);
      } catch (_) {
        /* ignore */
      }
    }
    state.tipLine = state.candle.createPriceLine({
      price: tip.close,
      color,
      lineWidth: 1,
      lineStyle: LightweightCharts.LineStyle.Dashed,
      axisLabelVisible: true,
      title: "",
    });
  }

  function paint(follow) {
    const slice = state.allBars.slice(0, state.cursor);
    state.candle.setData(slice.map(toCandle));
    state.volume.setData(slice.map(toVolume));
    updateTipMarker();
    if (!state.hovering) updateReadout(tipBar(), "tip");
    else el.clockDate.textContent = tipBar() ? String(tipBar().time) : "—";
    updateControls();
    if (follow) scheduleFollow();
    const tip = tipBar();
    if (tip) news.select(tip.time);
  }

  function setCursor(n, { fit = false, follow = true } = {}) {
    const next = Math.max(0, Math.min(state.allBars.length, n | 0));
    state.cursor = next;
    paint(follow && !fit);
    if (fit) {
      try {
        state.chart.timeScale().fitContent();
      } catch (_) {
        /* ignore */
      }
    }
  }

  function clearTimer() {
    if (state.timer != null) {
      clearTimeout(state.timer);
      state.timer = null;
    }
  }

  function pause() {
    state.playing = false;
    el.play.classList.remove("is-active");
    clearTimer();
    updateControls();
  }

  function advanceOne() {
    if (state.cursor >= state.allBars.length) {
      pause();
      setStatus("Playback complete");
      return false;
    }
    const bar = state.allBars[state.cursor];
    state.cursor += 1;
    state.candle.update(toCandle(bar));
    state.volume.update(toVolume(bar));
    updateTipMarker();
    scheduleFollow();
    if (!state.hovering) updateReadout(bar, "tip");
    el.clockDate.textContent = String(bar.time);
    updateControls();
    news.select(bar.time);
    return true;
  }

  function scheduleNext() {
    clearTimer();
    if (!state.playing) return;
    state.timer = setTimeout(() => {
      if (!advanceOne()) return;
      scheduleNext();
    }, BASE_TICK_MS / state.speed);
  }

  function play() {
    if (state.playing || state.cursor >= state.allBars.length) return;
    state.playing = true;
    el.play.classList.add("is-active");
    setStatus(`Playing @ ${state.speed}x`);
    updateControls();
    scheduleFollow();
    scheduleNext();
  }

  function step() {
    if (state.playing) return;
    if (!advanceOne()) return;
    setStatus("Stepped one bar");
  }

  function stepBack() {
    if (state.playing || state.cursor <= 0) return;
    setCursor(state.cursor - 1);
    setStatus("Stepped back");
  }

  function reset() {
    pause();
    const warm = Math.min(WARMUP, state.allBars.length);
    setCursor(warm, { fit: true, follow: false });
    setStatus(`Reset · ${warm}/${state.allBars.length} bars`);
  }

  function findBarByTime(t) {
    const key = String(t);
    return state.allBars.find((b) => String(b.time) === key) || null;
  }

  function initChart() {
    if (typeof LightweightCharts === "undefined") {
      setStatus("Failed to load Lightweight Charts");
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
      rightPriceScale: { borderColor: "#2a3038", scaleMargins: { top: 0.08, bottom: 0.22 } },
      timeScale: { borderColor: "#2a3038", timeVisible: false, rightOffset: 4, barSpacing: 8 },
      handleScroll: { vertTouchDrag: false },
    });

    state.candle = state.chart.addCandlestickSeries({
      upColor: "#26a69a",
      downColor: "#ef5350",
      borderUpColor: "#26a69a",
      borderDownColor: "#ef5350",
      wickUpColor: "#26a69a",
      wickDownColor: "#ef5350",
    });
    state.volume = state.chart.addHistogramSeries({
      priceFormat: { type: "volume" },
      priceScaleId: "vol",
    });
    state.chart.priceScale("vol").applyOptions({
      scaleMargins: { top: 0.82, bottom: 0 },
      borderVisible: false,
    });

    state.chart.subscribeCrosshairMove((param) => {
      if (!param || !param.time) {
        state.hovering = false;
        updateReadout(tipBar(), "tip");
        return;
      }
      const bar = findBarByTime(param.time);
      if (!bar) {
        state.hovering = false;
        updateReadout(tipBar(), "tip");
        return;
      }
      state.hovering = true;
      updateReadout(bar, "hover");
      el.clockDate.textContent = tipBar() ? String(tipBar().time) : "—";
    });

    state.chart.subscribeClick((param) => {
      if (!param || !param.time) return;
      const bar = findBarByTime(param.time);
      if (!bar) return;
      state.hovering = false;
      updateReadout(bar, "tip");
      news.select(bar.time, { status: setStatus });
    });

    new ResizeObserver(() => {
      const { width, height } = el.chart.getBoundingClientRect();
      state.chart.applyOptions({ width, height });
    }).observe(el.chart);
  }

  async function loadBars(symbol, interval) {
    pause();
    setStatus(`Loading ${symbol}…`);
    news.clearCache();
    const data = await TradeUI.fetchBars(symbol, interval);
    state.symbol = (data.symbol || symbol).toUpperCase();
    state.interval = data.interval || interval;
    state.source = data.source || "sample";
    state.allBars = (data.bars || []).map((b) => ({
      time: b.time || b.date,
      open: Number(b.open),
      high: Number(b.high),
      low: Number(b.low),
      close: Number(b.close),
      volume: Number(b.volume) || 0,
    }));
    state.hovering = false;
    const warm = Math.min(WARMUP, state.allBars.length);
    setCursor(warm, { fit: true, follow: false });
    setStatus(
      state.allBars.length
        ? `${warm}/${state.allBars.length} bars (${state.source}) — click candle for news`
        : "No bars"
    );
  }

  /** Hook for your RL / policy layer — override TradeUI.onAction. */
  function emitAction(action) {
    const tip = tipBar();
    const ctx = {
      action,
      symbol: state.symbol,
      interval: state.interval,
      bar: tip,
      cursor: state.cursor,
      date: tip ? tip.time : null,
    };
    if (typeof TradeUI.onAction === "function") {
      TradeUI.onAction(ctx);
    } else {
      setStatus(`${action.toUpperCase()} @ ${ctx.date || "—"} (wire TradeUI.onAction)`);
      console.debug("TradeUI action", ctx);
    }
  }

  function bindUI() {
    el.play.addEventListener("click", play);
    el.pause.addEventListener("click", pause);
    el.step.addEventListener("click", step);
    el.back.addEventListener("click", stepBack);
    el.reset.addEventListener("click", reset);

    const scrubTo = (n) => {
      setCursor(n, { follow: true });
      updateControls();
    };
    el.scrubber.addEventListener("pointerdown", () => {
      state.scrubbing = true;
      pause();
    });
    el.scrubber.addEventListener("input", () => scrubTo(Number(el.scrubber.value) || 0));
    el.scrubber.addEventListener("change", () => {
      state.scrubbing = false;
      scrubTo(Number(el.scrubber.value) || 0);
    });
    el.scrubber.addEventListener("pointerup", () => {
      state.scrubbing = false;
      scheduleFollow();
      updateControls();
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
          setStatus(err.message);
        }
      });
    });

    document.querySelectorAll("[data-action]").forEach((btn) => {
      btn.addEventListener("click", () => emitAction(btn.dataset.action));
    });

    window.addEventListener("keydown", (e) => {
      if (e.target && /input|textarea|select/i.test(e.target.tagName)) return;
      if (e.code === "Space") {
        e.preventDefault();
        state.playing ? pause() : play();
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
    try {
      await loadBars(state.symbol, state.interval);
    } catch (err) {
      setStatus(err.message);
      updateControls();
    }
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", boot);
  } else {
    boot();
  }
})();
