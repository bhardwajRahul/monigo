/*
 * Overview page renderer.
 *
 * Replaces the metric-tile and chart half of index.js, and the 300-second
 * `location.reload()` in refresh.js. The design's LIVE indicator implies
 * in-place updates, so a full page reload would make it a lie -- and reloading
 * also threw away expanded state and navigated to the browser error page at
 * exactly the moment the monitored service died.
 *
 * Every value drawn here is traced to a real API field. Where the design shows
 * something MoniGo does not measure, the element is omitted rather than filled
 * with a plausible number: see DESIGN-ADOPTION.md.
 */
document.addEventListener('DOMContentLoaded', () => {
    'use strict';

    // ------------------------------------------------------------ transport

    /*
     * common.js defines these inside its own DOMContentLoaded closure, so they
     * are not visible here. Duplicated rather than exported, because making
     * them global would put the API key on `window` where any script on the
     * page could read it.
     */
    function getApiKey() {
        const params = new URLSearchParams(window.location.search);
        return params.get('api_key') || window.MONIGO_API_KEY || null;
    }

    function authenticatedFetch(url, options = {}) {
        const key = getApiKey();
        if (!key) {
            return fetch(url, options);
        }
        const headers = Object.assign({}, options.headers, { 'X-API-Key': key });
        return fetch(url, Object.assign({}, options, { headers }));
    }

    // ---------------------------------------------------------------- guards

    /*
     * Several fields on this page are unit-suffixed display strings that sit
     * next to a near-identically named numeric twin -- memory_used_by_service
     * beside memory_used_by_service_bytes, and so on. parseFloat("1.2 GB")
     * returns 1.2 and silently plots gigabytes against megabytes. That bug
     * shipped once already.
     *
     * num() makes it impossible by construction rather than by review: a
     * string carrying anything other than digits is refused outright.
     */
    function num(value, where) {
        if (typeof value === 'number') {
            return isFinite(value) ? value : NaN;
        }
        if (typeof value !== 'string') {
            return NaN;
        }
        if (!/^-?\d+(\.\d+)?$/.test(value.trim())) {
            console.error(
                `[monigo] refusing to parse ${JSON.stringify(value)} as a number` +
                (where ? ` for ${where}` : '') +
                '. It carries a unit suffix; use the *_bytes or raw field instead.'
            );
            return NaN;
        }
        return Number(value);
    }

    // A percent string ("0.26%") has no numeric twin -- ServiceCPULoadRaw is
    // json:"-" -- so this is the one suffix we strip, and only this one.
    function percent(value) {
        return num(String(value == null ? '' : value).replace(/%\s*$/, ''), 'percent');
    }

    // -------------------------------------------------------------- format

    function formatBytes(bytes) {
        if (!isFinite(bytes) || bytes < 0) {
            return '—';
        }
        const units = ['B', 'KB', 'MB', 'GB', 'TB'];
        let v = bytes;
        let i = 0;
        while (v >= 1024 && i < units.length - 1) {
            v /= 1024;
            i++;
        }
        return trimZeros(v.toFixed(2)) + ' ' + units[i];
    }

    // raw_mem_stats_records are base-1024 KB, not bytes (core.go newRawRecord).
    function formatKB(kb) {
        if (!isFinite(kb)) {
            return '—';
        }
        return kb >= 1024
            ? trimZeros((kb / 1024).toFixed(2)) + ' MB'
            : trimZeros(kb.toFixed(2)) + ' KB';
    }

    function trimZeros(s) {
        return String(s).replace(/\.00$/, '');
    }

    function set(id, text) {
        const el = document.getElementById(id);
        if (el) {
            el.textContent = text;
        }
    }

    function severityClass(el, level) {
        if (!el) {
            return;
        }
        el.classList.remove('mg-sev-ok', 'mg-sev-warn', 'mg-sev-crit');
        el.classList.add('mg-sev-' + level);
    }

    // --------------------------------------------------------------- state

    const RING_CIRCUMFERENCE = 232.5;   // 2 * PI * r, r = 37
    const state = {
        live: true,
        range: '15m',
        timer: null,
        lastGood: null,
        syncFrequency: '5m',
    };

    // -------------------------------------------------------------- health

    function renderHealth(m) {
        const svc = (m.health && m.health.service_health) || {};
        const sys = (m.health && m.health.system_health) || {};

        const pct = num(svc.percent, 'service_health.percent');
        if (isFinite(pct)) {
            set('mg-ring-value', pct.toFixed(1));
            const arc = document.getElementById('mg-ring-arc');
            if (arc) {
                const filled = Math.max(0, Math.min(100, pct)) / 100 * RING_CIRCUMFERENCE;
                arc.setAttribute('stroke-dasharray', filled.toFixed(1) + ' ' + RING_CIRCUMFERENCE);
                /*
                 * Colour follows the server's own verdict, not a threshold
                 * invented here. The server holds the configured limits --
                 * MaxCPUUsage and friends -- so it is the only thing that knows
                 * what "healthy" means for this service.
                 *
                 * This used to shade the ring on a 70/50 split of its own,
                 * which produced a brown ring beside the word "Healthy" at
                 * 68.4%: two answers to the same question, side by side. The
                 * arc length already carries the degree; the colour carries the
                 * verdict.
                 */
                severityClass(arc, svc.healthy === false ? 'crit' : 'ok');
            }
        }

        renderState('mg-svc-state', svc);
        renderState('mg-sys-state', sys);

        const stateEl = document.getElementById('mg-svc-state');
        if (stateEl && svc.message) {
            stateEl.title = svc.message;
        }

        // The service detail line and both threshold bars are scraped from
        // icon_msg, which is the only place the configured thresholds surface:
        // MaxCPUUsage and friends are never serialised on their own.
        const svcMsg = svc.icon_msg || '';
        const cpu = /CPU Usage ([\d.]+)% \/ ([\d.]+)%/.exec(svcMsg);
        const mem = /Memory Usage ([\d.]+)% \/ ([\d.]+)%/.exec(svcMsg);
        const gor = /Goroutines ([\d.]+) \/ (\d+)/.exec(svcMsg);
        const parts = [];
        if (cpu) {
            parts.push(`CPU ${(+cpu[1]).toFixed(2)}% / ${(+cpu[2]).toFixed(0)}%`);
        }
        if (mem) {
            parts.push(`Memory ${(+mem[1]).toFixed(2)}% / ${(+mem[2]).toFixed(0)}%`);
        }
        if (gor) {
            parts.push(`Goroutines ${Math.round(+gor[1])} / ${gor[2]}`);
        }
        set('mg-svc-detail', parts.join('  ·  '));

        const sysMsg = sys.icon_msg || '';
        bar('cpu', /CPU Usage ([\d.]+)% \/ ([\d.]+)%/.exec(sysMsg));
        bar('mem', /Memory Usage ([\d.]+)% \/ ([\d.]+)%/.exec(sysMsg));

    }

    function renderState(id, health) {
        const el = document.getElementById(id);
        if (!el) {
            return;
        }
        const healthy = health.healthy !== false;
        el.textContent = healthy ? 'Healthy' : 'Degraded';
        severityClass(el, healthy ? 'ok' : 'crit');
    }

    // Draws one threshold bar. The fill is the usage as a proportion of the
    // limit, so a bar that reaches the end IS the breach -- no separate marker
    // needed, and a value over 100% clamps visually while still reading true.
    function bar(which, match) {
        const valueEl = document.getElementById(`mg-bar-${which}-value`);
        const fillEl = document.getElementById(`mg-bar-${which}-fill`);
        if (!match) {
            if (valueEl) {
                valueEl.textContent = '—';
            }
            if (fillEl) {
                fillEl.style.width = '0%';
            }
            return;
        }
        const used = +match[1];
        const limit = +match[2];
        if (valueEl) {
            valueEl.textContent = `${used.toFixed(2)}% / ${limit.toFixed(2)}%`;
            severityClass(valueEl, used >= limit ? 'crit' : used >= limit * 0.8 ? 'warn' : 'ok');
        }
        if (fillEl) {
            const ratio = limit > 0 ? used / limit : 0;
            fillEl.style.width = Math.max(0, Math.min(1, ratio)) * 100 + '%';
            severityClass(fillEl, ratio >= 1 ? 'crit' : ratio >= 0.8 ? 'warn' : 'ok');
        }
    }

    // ----------------------------------------------------------- KPI tiles

    function rawRecords(m) {
        const out = {};
        const list = (m.memory_statistics && m.memory_statistics.raw_mem_stats_records) || [];
        list.forEach(r => {
            out[r.record_name] = r.record_value;
        });
        return out;
    }

    /*
     * Per-tile history, kept in the page.
     *
     * The stored series is the authoritative one, but it is flushed on
     * DataPointsSyncFrequency -- five minutes by default -- so asking storage
     * every fifteen seconds returns the same points over and over. These tiles
     * want "the last few minutes as observed", which is exactly what the poll
     * loop already has in hand. The chart below asks storage, because it wants
     * history that predates this page load.
     *
     * Capped, and lost on reload: it is a shape, not a record.
     */
    const hist = { cpu: [], heap: [], gor: [], gc: [] };
    const HIST_CAP = 40;

    function pushHist(key, value) {
        if (typeof value !== 'number' || !isFinite(value)) {
            return;
        }
        hist[key].push(value);
        if (hist[key].length > HIST_CAP) {
            hist[key].shift();
        }
    }

    // Writes into the two <path> elements the tile already carries, rather than
    // replacing the SVG, so nothing reflows on each poll.
    function drawSpark(prefix, values) {
        const area = document.getElementById(prefix + '-area');
        const line = document.getElementById(prefix + '-line');
        if (!area || !line) {
            return;
        }
        const pts = values.filter(v => typeof v === 'number' && isFinite(v));
        if (pts.length < 2) {
            area.removeAttribute('d');
            line.removeAttribute('d');
            return;
        }
        const W = 240;
        const H = 44;
        const lo = Math.min(...pts);
        const hi = Math.max(...pts);
        const span = (hi - lo) || 1;
        const d = pts.map((v, i) => {
            const x = (i / (pts.length - 1)) * W;
            const y = H - 4 - ((v - lo) / span) * (H - 8);
            return (i ? 'L' : 'M') + x.toFixed(1) + ' ' + y.toFixed(1);
        }).join(' ');
        line.setAttribute('d', d);
        area.setAttribute('d', d + ` L${W} ${H} L0 ${H} Z`);
        // Colour comes from CSS: each tile sets --mg-series, and .mg-spark__line
        // / .mg-spark__area read it. Setting stroke here as a presentation
        // attribute would lose to that rule anyway, and would be a second place
        // to keep the palette in step.
    }

    function renderKPIs(m) {
        const raw = rawRecords(m);

        const cpu = percent(m.load_statistics && m.load_statistics.service_cpu_load);
        set('mg-kpi-cpu-value', isFinite(cpu) ? cpu.toFixed(3) : '—');

        // heap_inuse / heap_sys are base-1024 KB.
        const inuse = num(raw.heap_inuse, 'heap_inuse');
        const sys = num(raw.heap_sys, 'heap_sys');
        set('mg-kpi-heap-value', isFinite(inuse) ? (inuse / 1024).toFixed(2) : '—');
        set('mg-kpi-heap-total', isFinite(sys) ? 'of ' + (sys / 1024).toFixed(2) : '');

        const goroutines = m.core_statistics && m.core_statistics.goroutines;
        set('mg-kpi-gor-value', goroutines == null ? '—' : String(goroutines));

        // Stale goroutines are only worth a word when there are some.
        const stale = (m.goroutine_leak_report && m.goroutine_leak_report.stale_goroutines) || 0;
        const staleEl = document.getElementById('mg-kpi-gor-stale');
        if (staleEl) {
            staleEl.textContent = stale > 0 ? stale + ' stale' : '';
            staleEl.classList.toggle('is-warn', stale > 0);
        }

        // gc_pause_duration_ms is PauseTotalNs/1e6 -- cumulative since start,
        // which is why the tile is labelled GC PAUSE TOTAL rather than
        // presenting a running total as a latest pause.
        const gc = num(m.memory_statistics && m.memory_statistics.gc_pause_duration_ms, 'gc_pause_duration_ms');
        set('mg-kpi-gc-value', isFinite(gc) ? gc.toFixed(2) : '—');
        // num_gc is a count, not a byte metric, so it is not scaled by 1024.
        const cycles = num(raw.num_gc, 'num_gc');
        set('mg-kpi-gc-cycles', isFinite(cycles) ? Math.round(cycles) + ' cycles' : '');

        pushHist('cpu', cpu);
        pushHist('heap', isFinite(inuse) ? inuse / 1024 : NaN);
        pushHist('gor', typeof goroutines === 'number' ? goroutines : NaN);
        pushHist('gc', gc);

        drawSpark('mg-kpi-cpu', hist.cpu);
        drawSpark('mg-kpi-heap', hist.heap);
        drawSpark('mg-kpi-gor', hist.gor);
        drawSpark('mg-kpi-gc', hist.gc);

        // A delta needs something to compare against; five polls is ~75s.
        const deltaEl = document.getElementById('mg-kpi-cpu-delta');
        if (deltaEl) {
            if (hist.cpu.length > 5) {
                const d = hist.cpu[hist.cpu.length - 1] - hist.cpu[hist.cpu.length - 6];
                deltaEl.textContent = (d >= 0 ? '+' : '') + d.toFixed(3);
            } else {
                deltaEl.textContent = '';
            }
        }

        renderLeakBanner(m.goroutine_leak_report);
    }

    /*
     * The leak verdict is surfaced here as well as on the Goroutines page. It
     * is the one finding an operator should not have to go looking for, and it
     * only appears when there is something to say -- a banner that is always
     * present stops being read.
     */
    function renderLeakBanner(report) {
        const box = document.getElementById('mg-leak');
        if (!box) {
            return;
        }
        if (!report || !report.leak_suspected) {
            box.hidden = true;
            box.textContent = '';
            return;
        }
        box.hidden = false;
        box.className = 'mg-note mg-note--warn';
        box.innerHTML =
            '<svg class="mg-icon" aria-hidden="true"><use href="#i-alert-triangle"></use></svg>' +
            '<span></span>' +
            '<a class="mg-note__act" href="./go-routines-stats.html">Inspect</a>';
        box.querySelector('span').textContent =
            report.message || 'Goroutine leak suspected.';
    }

    // -------------------------------------------------------- heap panel

    function renderHeap(m) {
        const raw = rawRecords(m);
        const inuse = num(raw.heap_inuse, 'heap_inuse');
        const alloc = num(raw.alloc, 'alloc');
        const idle = num(raw.heap_idle, 'heap_idle');
        const released = num(raw.heap_released, 'heap_released');

        /*
         * Go's heap partitions exactly: HeapSys = HeapInuse + HeapIdle. Those
         * two are the whole of it, and the only two that belong in the bar.
         *
         * Alloc (== HeapAlloc) is live objects *inside* the in-use spans, and
         * heap_released is a subset of the idle ones. Including either counts
         * the same bytes twice -- with Alloc in the denominator the bar read
         * three near-equal thirds and overstated the heap by 50%, because Alloc
         * tracks HeapInuse closely. Both stay in the legend as the nested
         * quantities they are.
         */
        const total = [inuse, idle].reduce((a, b) => a + (isFinite(b) ? b : 0), 0);
        const seg = (id, v) => {
            const el = document.getElementById(id);
            if (el) {
                el.style.width = total > 0 && isFinite(v) ? (v / total * 100).toFixed(2) + '%' : '0%';
            }
        };
        seg('mg-heap-seg-inuse', inuse);
        seg('mg-heap-seg-idle', idle);

        set('mg-heap-val-inuse', formatKB(inuse));
        set('mg-heap-val-alloc', formatKB(alloc));
        set('mg-heap-val-idle', formatKB(idle));
        set('mg-heap-val-released', formatKB(released));

        const io = m.network_io || {};
        set('mg-readout-net-sent', formatBytes(num(io.bytes_sent, 'bytes_sent')));
        set('mg-readout-net-recv', formatBytes(num(io.bytes_received, 'bytes_received')));

        const mem = m.memory_statistics || {};
        set('mg-readout-stack', formatBytes(num(mem.stack_memory_usage_bytes, 'stack_memory_usage_bytes')));
        // free_swap_memory is the one field with no numeric twin. Printed
        // verbatim; never parsed.
        set('mg-readout-swap-free', mem.free_swap_memory ? trimZeros(mem.free_swap_memory) : '—');
    }

    function renderReadouts(m, info) {
        set('mg-readout-uptime', (m.core_statistics && m.core_statistics.uptime) || '—');
        const cores = num(m.cpu_statistics && m.cpu_statistics.total_cores, 'total_cores');
        set('mg-readout-cores', isFinite(cores) ? String(Math.round(cores)) : '—');
        if (info) {
            set('mg-readout-pid', info.process_id == null ? '—' : String(info.process_id));
            set('mg-readout-go', (info.go_version || '—').replace(/^go/, ''));
        }
    }

    // ------------------------------------------------------------ freshness

    // The stamp is the time of the last SUCCESSFUL poll, frozen on failure, so

    // MG owns the connection state, the banner and the as-of stamp. This only
    // needs to say what failed, and let the rejection reach MG.Poll.
    function markStale(err) {
        console.error('[monigo] metrics poll failed:', err);
    }

    // -------------------------------------------------------------- polling

    function poll() {
        return Promise.all([
            authenticatedFetch('/monigo/api/v1/metrics').then(r => {
                if (!r.ok) {
                    throw new Error('metrics HTTP ' + r.status);
                }
                return r.json();
            }),
            authenticatedFetch('/monigo/api/v1/service-info').then(r => r.ok ? r.json() : null).catch(() => null),
        ])
            .then(([m, info]) => {
                renderHealth(m);
                renderKPIs(m);
                renderHeap(m);
                renderReadouts(m, info);
            })
            .catch(err => {
                markStale(err);
                // Rethrow: MG.Poll decides stale-vs-down from this.
                throw err;
            });
    }

    function startPolling() {
        // MG.Poll owns the interval, the visibility check and the ok/stale/down
        // escalation, so every page reports a failure the same way. poll()
        // returns its promise; a rejection is what tells MG the server stopped
        // answering.
        MG.Poll.start(poll, 15000);
    }

    function stopPolling() {
        if (state.timer) {
            clearInterval(state.timer);
            state.timer = null;
        }
    }

    // ---------------------------------------------------------- top bar UI

    function wireLive() {
        const btn = document.getElementById('mg-live');
        if (!btn) {
            return;
        }
        btn.addEventListener('click', () => {
            state.live = !state.live;
            btn.classList.toggle('is-live', state.live);
            btn.setAttribute('aria-pressed', String(state.live));
            set('mg-live-label', state.live ? 'LIVE' : 'PAUSED');
            if (state.live) {
                poll();
                startPolling();
            } else {
                stopPolling();
            }
        });
    }

    function wireRange() {
        const group = document.getElementById('mg-range');
        if (!group) {
            return;
        }
        group.addEventListener('click', (e) => {
            const seg = e.target.closest('.mg-range-seg');
            if (!seg) {
                return;
            }
            state.range = seg.dataset.range;
            [...group.querySelectorAll('.mg-range-seg')].forEach(s => {
                const on = s === seg;
                s.classList.toggle('is-active', on);
                s.setAttribute('aria-pressed', String(on));
            });
            loadSeries();
        });
    }

    // ------------------------------------------------------ history series

    const RANGE_MINUTES = { '5m': 5, '15m': 15, '1h': 60, '24h': 1440 };

    function loadSeries() {
        const mins = RANGE_MINUTES[state.range] || 15;
        const end = new Date();
        const start = new Date(end.getTime() - mins * 60000);
        const body = {
            field_name: ['service_cpu_load', 'heap_inuse', 'goroutines'],
            timeframe: state.range,
            start_time: toUTCISO(start),
            end_time: toUTCISO(end),
        };

        authenticatedFetch('/monigo/api/v1/service-metrics', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(body),
        })
            .then(r => {
                if (!r.ok) {
                    throw new Error('service-metrics responded ' + r.status);
                }
                return r.json();
            })
            .then(rows => drawSeries(Array.isArray(rows) ? rows : []))
            .catch(err => {
                console.error('[monigo] runtime load series failed:', err);
                drawSeries(null);
            });
    }

    /*
     * Datapoints are stored against real Unix timestamps, and the handler
     * parses this string as RFC3339. index.js formatted *local* time and then
     * appended "Z", which claims UTC -- so the server searched a window offset
     * by the machine's timezone and returned 500 for every non-UTC host. The
     * chart has to send true UTC.
     */
    function toUTCISO(d) {
        return d.toISOString().replace(/\.\d{3}Z$/, 'Z');
    }

    /*
     * The API re-keys field names on the way out (api.NameMap), so heap_inuse
     * comes back as HeapInuse. Reading the request-side name yields undefined,
     * which becomes NaN, which draws an empty chart that looks like "no data
     * yet" rather than a bug. Check both spellings.
     */
    function pick(value, ...names) {
        for (const n of names) {
            if (value && value[n] != null) {
                return num(value[n], n);
            }
        }
        return NaN;
    }

    // rows === null means the fetch failed. That is a different message from
    // "the window is genuinely short", and conflating the two hid a real bug.
    function drawSeries(rows) {
        const empty = document.getElementById('mg-runtime-load-empty');
        const canvas = document.getElementById('mg-runtime-load-chart');
        const short = document.getElementById('mg-runtime-load-short');
        const failed = document.getElementById('mg-runtime-load-failed');
        set('mg-sync-freq', state.syncFrequency);

        if (rows === null) {
            if (empty) {
                empty.hidden = false;
            }
            if (short) {
                short.hidden = true;
            }
            if (failed) {
                failed.hidden = false;
            }
            if (canvas) {
                canvas.style.visibility = 'hidden';
            }
            return;
        }
        if (failed) {
            failed.hidden = true;
        }
        if (short) {
            short.hidden = false;
        }

        // Two points cannot show a trend. Samples are written every
        // DataPointsSyncFrequency (5m by default), so a 15m window holds three
        // -- saying so is better than drawing a straight line and letting the
        // reader infer stability that was never measured.
        if (rows.length < 2) {
            if (empty) {
                empty.hidden = false;
            }
            if (short) {
                // How far along it is, rather than only that it is not ready.
                // Waiting for the first samples is the ordinary state of a
                // process that just started, not a fault.
                const have = rows.length;
                short.innerHTML = '';
                short.appendChild(document.createTextNode(
                    have === 0
                        ? 'No samples stored yet. '
                        : `${have} of 2 samples so far. `));
                const b = document.createElement('b');
                b.textContent = state.syncFrequency;
                short.appendChild(document.createTextNode('One is written every '));
                short.appendChild(b);
                short.appendChild(document.createTextNode(
                    ', so a line appears once there are two.'));
            }
            // The grid still draws, so the card reads as a chart waiting for
            // data rather than as an empty box.
            if (canvas) {
                canvas.style.visibility = '';
                drawEmptyGrid(canvas);
            }
            return;
        }
        if (empty) {
            empty.hidden = true;
        }
        if (canvas) {
            canvas.style.visibility = '';
        }

        const times = rows.map(r => new Date(r.time).toTimeString().slice(0, 5));
        const cpu = rows.map(r => pick(r.value, 'service_cpu_load', 'ServiceCPULoad'));
        const heap = rows.map(r => {
            const kb = pick(r.value, 'heap_inuse', 'HeapInuse');
            return isFinite(kb) ? +(kb / 1024).toFixed(2) : null;
        });
        const gor = rows.map(r => pick(r.value, 'goroutines', 'Goroutines'));

        renderChart(canvas, times, cpu, heap, gor, rows.length);
    }

    function cssVar(name) {
        return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
    }

    /*
     * The runtime-load chart, drawn as inline SVG.
     *
     * This replaced ECharts, which was 1.0 MB of the embedded payload for this
     * one chart -- a cost every consumer paid in their binary whether or not
     * they ever opened the dashboard.
     *
     * The three series are normalised INDEPENDENTLY rather than sharing a y
     * axis. They have unrelated units -- a CPU percentage, megabytes of heap,
     * and a goroutine count -- so a shared axis lets whichever is numerically
     * largest flatten the other two into the baseline: with heap at 778 and
     * goroutines at 14, the CPU line is a straight line at zero regardless of
     * what it did. The question this chart exists to answer is "which moved
     * first", which is a question about shape, so each line gets the full
     * height and the y axis carries no labels. Absolute values are on the KPI
     * tiles directly above, and on the crosshair readout.
     */
    const PLOT = { w: 600, h: 200, pad: 10 };

    // The same grid the plot draws, with no series on it. Keeping the card the
    // shape of a chart while it waits is less jarring than a void, and it makes
    // the axis-free design legible before there is anything to read.
    function drawEmptyGrid(el) {
        const grid = cssVar('--grid-line');
        const lines = [0, 0.25, 0.5, 0.75]
            .map(f => {
                const y = (PLOT.pad + f * (PLOT.h - PLOT.pad * 2)).toFixed(1);
                return `<line x1="0" y1="${y}" x2="${PLOT.w}" y2="${y}" ` +
                    `stroke="${grid}" stroke-width="1" opacity="0.45"/>`;
            })
            .join('');
        el.innerHTML =
            `<svg class="mg-plot__svg" viewBox="0 0 ${PLOT.w} ${PLOT.h}" ` +
            `preserveAspectRatio="none" aria-hidden="true">${lines}</svg>`;
    }

    function renderChart(el, times, cpu, heap, gor, sampleCount) {
        if (!el) {
            return;
        }

        /*
         * Each series is drawn differently on purpose, so the three stay
         * separable where they cross -- which is the whole point of putting
         * them on one axis. Only service cpu carries an area fill: three
         * translucent fills over one another turn the plot to mud, and cpu is
         * the series the eye should land on first. Goroutines is dashed
         * because it is a step count rather than a continuous quantity.
         */
        const series = [
            {
                key: 'cpu', label: 'service cpu', values: cpu,
                color: cssVar('--series-1'), width: 1.8, area: true,
                fmt: v => v.toFixed(3) + '%',
            },
            {
                key: 'heap', label: 'heap inuse', values: heap,
                color: cssVar('--series-2'), width: 1.6, area: false,
                fmt: v => v.toFixed(2) + ' MB',
            },
            {
                key: 'gor', label: 'goroutines', values: gor,
                color: cssVar('--series-3'), width: 1.4, area: false, dash: '4 3',
                fmt: v => String(Math.round(v)),
            },
        ];

        // Fewer than ~6 samples reads as a polyline, not a trend, so the points
        // are drawn explicitly rather than implied by the line.
        const sparse = sampleCount < 6;
        const grid = cssVar('--grid-line');

        const gridLines = [0, 0.25, 0.5, 0.75]
            .map(f => {
                const y = (PLOT.pad + f * (PLOT.h - PLOT.pad * 2)).toFixed(1);
                return `<line x1="0" y1="${y}" x2="${PLOT.w}" y2="${y}" ` +
                    `stroke="${grid}" stroke-width="1" opacity="0.6"/>`;
            })
            .join('');

        const paths = series.map(sr => {
            const d = linePath(sr.values);
            if (!d) {
                return '';
            }
            let out = '';
            if (sr.area && !sparse) {
                out += `<path d="${areaPath(sr.values)}" fill="${sr.color}" opacity="0.10"/>`;
            }
            out += `<path d="${d}" fill="none" stroke="${sr.color}" stroke-width="${sr.width}" ` +
                (sr.dash ? `stroke-dasharray="${sr.dash}" ` : '') +
                `vector-effect="non-scaling-stroke" stroke-linejoin="round"/>`;
            if (sparse) {
                // pointsOf yields y === null where the sample is missing, so a
                // gap stays a gap rather than being drawn at zero. Those have
                // no dot to place.
                out += pointsOf(sr.values)
                    .filter(pt => pt.y !== null)
                    .map(pt => `<circle cx="${pt.x.toFixed(1)}" cy="${pt.y.toFixed(1)}" r="2.5" fill="${sr.color}"/>`)
                    .join('');
            }
            return out;
        }).join('');

        el.innerHTML =
            `<svg class="mg-plot__svg" viewBox="0 0 ${PLOT.w} ${PLOT.h}" preserveAspectRatio="none" ` +
            `aria-hidden="true">${gridLines}${paths}` +
            `<line class="mg-plot__cross" x1="0" y1="0" x2="0" y2="${PLOT.h}" stroke="${cssVar('--ink-subtle')}" ` +
            `stroke-width="1" stroke-dasharray="3 3" opacity="0"/></svg>` +
            `<div class="mg-plot__readout" aria-hidden="true"></div>`;

        wireCrosshair(el, times, series);
    }

    // Scales one series across the full plot height. Returns null-safe points so
    // a gap in the data stays a gap rather than being interpolated over.
    function pointsOf(values) {
        const finite = values.filter(v => typeof v === 'number' && isFinite(v));
        if (finite.length < 2) {
            return [];
        }
        const lo = Math.min(...finite);
        const hi = Math.max(...finite);
        const span = (hi - lo) || 1;
        const inner = PLOT.h - PLOT.pad * 2;
        return values.map((v, i) => ({
            x: (i / (values.length - 1)) * PLOT.w,
            y: typeof v === 'number' && isFinite(v)
                ? PLOT.h - PLOT.pad - ((v - lo) / span) * inner
                : null,
        }));
    }

    function linePath(values) {
        const pts = pointsOf(values);
        if (!pts.length) {
            return '';
        }
        let d = '';
        let penDown = false;
        pts.forEach(pt => {
            if (pt.y === null) {
                penDown = false;   // a gap, not a value of zero
                return;
            }
            d += `${penDown ? 'L' : 'M'}${pt.x.toFixed(1)} ${pt.y.toFixed(1)} `;
            penDown = true;
        });
        return d.trim();
    }

    function areaPath(values) {
        const d = linePath(values);
        if (!d) {
            return '';
        }
        return `${d} L${PLOT.w} ${PLOT.h} L0 ${PLOT.h} Z`;
    }

    // Replaces the ECharts tooltip. Without it the chart shows shape but no
    // numbers, and the y axis deliberately carries none.
    function wireCrosshair(el, times, series) {
        const svg = el.querySelector('.mg-plot__svg');
        const cross = el.querySelector('.mg-plot__cross');
        const readout = el.querySelector('.mg-plot__readout');
        if (!svg || !cross || !readout || !times.length) {
            return;
        }

        const show = clientX => {
            const box = el.getBoundingClientRect();
            if (!box.width) {
                return;
            }
            const frac = Math.min(1, Math.max(0, (clientX - box.left) / box.width));
            const i = Math.round(frac * (times.length - 1));

            cross.setAttribute('x1', ((i / Math.max(1, times.length - 1)) * PLOT.w).toFixed(1));
            cross.setAttribute('x2', cross.getAttribute('x1'));
            cross.setAttribute('opacity', '1');

            readout.innerHTML =
                `<span class="mg-plot__at">${times[i]}</span>` +
                series.map(sr => {
                    const v = sr.values[i];
                    const shown = typeof v === 'number' && isFinite(v) ? sr.fmt(v) : '—';
                    return `<span class="mg-plot__val" style="color:${sr.color}">${shown}</span>`;
                }).join('');
            readout.classList.add('is-visible');
        };

        el.addEventListener('mousemove', e => show(e.clientX));
        el.addEventListener('mouseleave', () => {
            cross.setAttribute('opacity', '0');
            readout.classList.remove('is-visible');
        });
    }


    // --------------------------------------------------- traced functions

    function loadFunctions() {
        authenticatedFetch('/monigo/api/v1/function')
            .then(r => r.ok ? r.json() : {})
            .then(renderFunctions)
            .catch(() => renderFunctions({}));
    }

    function renderFunctions(fns) {
        const rows = document.getElementById('mg-hot-rows');
        const empty = document.getElementById('mg-hot-empty');
        const tpl = document.getElementById('mg-hot-row-tpl');
        if (!rows || !tpl) {
            return;
        }
        const names = Object.keys(fns || {});
        set('mg-hot-count', String(names.length));
        rows.innerHTML = '';

        if (!names.length) {
            if (empty) {
                empty.hidden = false;
            }
            return;
        }
        if (empty) {
            empty.hidden = true;
        }

        // Sorted by the most recent execution time. There is no latency
        // distribution to rank by -- FunctionMetrics keeps only the latest
        // sample -- so the column is labelled LAST EXEC, not P95.
        names
            .map(name => ({ name, m: fns[name] || {} }))
            .sort((a, b) => execMs(b.m) - execMs(a.m))
            .slice(0, 8)
            .forEach(({ name, m }) => {
                const node = tpl.content.cloneNode(true);
                const short = name.split('/').pop();
                node.querySelector('.mg-fn-name').textContent = short;
                const pkg = node.querySelector('.mg-fn-pkg');
                if (pkg) {
                    pkg.textContent = short === name ? '' : name.slice(0, name.length - short.length);
                }
                node.querySelector('.mg-hot__exec').textContent = formatDuration(execMs(m));
                // An em dash here used to mean "zero", which conflated a
                // function that allocated nothing with one whose allocation was
                // never sampled -- profiling runs on one call in SamplingRate.
                // The flag separates them, and the bytes get formatted rather
                // than printed raw.
                const memCell = node.querySelector('.mg-hot__mem');
                if (m.memory_usage_sampled === true) {
                    memCell.textContent = formatBytes(Number(m.memory_usage) || 0);
                    memCell.title = 'Heap delta around the last sampled call';
                } else {
                    memCell.textContent = '—';
                    memCell.title = 'Not sampled yet. Profiling runs on one call in SamplingRate.';
                }
                node.querySelector('.mg-hot__last').textContent = m.function_last_ran || '—';
                rows.appendChild(node);
            });
    }

    function execMs(m) {
        const raw = m && (m.execution_time || m.ExecutionTime);
        if (typeof raw === 'number') {
            return raw / 1e6;   // Go duration, nanoseconds
        }
        const parsed = num(raw, 'execution_time');
        return isFinite(parsed) ? parsed / 1e6 : 0;
    }

    function formatDuration(ms) {
        if (!isFinite(ms) || ms <= 0) {
            return '—';
        }
        if (ms >= 1000) {
            return trimZeros((ms / 1000).toFixed(2)) + 's';
        }
        if (ms >= 1) {
            return trimZeros(ms.toFixed(2)) + 'ms';
        }
        return trimZeros((ms * 1000).toFixed(0)) + 'µs';
    }

    // ----------------------------------------------------------------- init

    function init() {
        // The service name appears in the top bar as well as the sidebar.
        authenticatedFetch('/monigo/api/v1/service-info')
            .then(r => r.ok ? r.json() : null)
            .then(info => {
                if (!info) {
                    return;
                }
                set('mg-service-name', info.service_name || '—');
                set('mg-page-subtitle', info.service_name
                    ? 'runtime · ' + info.service_name
                    : 'runtime');
            })
            .catch(() => { /* the status dot carries the failure */ });

        const year = document.getElementById('mg-footer-year');
        if (year) {
            year.textContent = new Date().getFullYear();
        }

        wireLive();
        wireRange();
        poll();
        startPolling();
        loadSeries();
        loadFunctions();

        // Charts read their colours from the tokens, so a theme change needs a
        // redraw rather than a reload.
        document.addEventListener('monigoThemeChanged', () => {
            loadSeries();
        });
    }

    init();
});
