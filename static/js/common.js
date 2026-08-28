document.addEventListener('DOMContentLoaded', () => {
    const refreshHtml = `
        <div class="loader-container">
            <div class="bouncing-dots">
                <div class="dot"></div>
                <div class="dot"></div>
                <div class="dot"></div>
            </div>
        </div>`;

    // Function to get API key from URL parameters
    function getApiKey() {
        const urlParams = new URLSearchParams(window.location.search);
        return urlParams.get('api_key');
    }

    // Function to make authenticated fetch request
    function authenticatedFetch(url, options = {}) {
        const apiKey = getApiKey();
        if (apiKey) {
            /*
             * The key goes in a header, never the URL. As a query parameter it
             * lands in browser history, in the Referer sent to any external
             * link, and in every access log between here and the process.
             * APIKeyMiddleware accepts either form, so the header suffices.
             */
            options.headers = Object.assign({}, options.headers, { 'X-API-Key': apiKey });
        }
        /*
         * No other credential is attached. This used to assert a privileged
         * role header on every unauthenticated request, copied from
         * example/security-examples/custom-auth -- whose auth function grants
         * access for exactly that header. The dashboard could therefore satisfy
         * a consumer's own auth check on its own say-so. A browser cannot be
         * trusted to assert its own privilege level, so it no longer claims
         * one; custom auth belongs in the middleware, not in page JavaScript.
         *
         * Basic auth needs nothing here -- the browser attaches credentials.
         */
        return fetch(url, options);
    }

    /*
     * Theme.
     *
     * The stylesheet keys off body[data-theme]. Light is the default, and the
     * markup carries data-theme="light" so a first visit paints it directly
     * rather than flashing the :root palette before this runs.
     *
     * The old class-based mechanism (body.dark-theme) and the toggle injected
     * into the vendored navbar both went with that navbar. The storage key and
     * its 'dark'/'light' values are unchanged, so a preference set before this
     * change is still honoured.
     */
    function setTheme(theme) {
        document.body.setAttribute('data-theme', theme);
        try {
            localStorage.setItem('monigo-theme', theme);
        } catch (e) {
            /* private mode: the toggle still works, it just will not persist */
        }
        setThemeIcon(theme === 'dark' ? '#i-sun' : '#i-moon');
        document.dispatchEvent(new CustomEvent('monigoThemeChanged', { detail: { theme } }));
    }

    function setThemeIcon(href) {
        const icon = document.getElementById('mg-theme-icon');
        if (icon) {
            icon.setAttribute('href', href);
        }
    }

    let savedTheme = 'light';
    try {
        savedTheme = localStorage.getItem('monigo-theme') || 'light';
    } catch (e) {
        /* ignore */
    }

    const themeToggle = document.getElementById('mg-theme-toggle');
    if (themeToggle) {
        themeToggle.addEventListener('click', () => {
            setTheme(document.body.getAttribute('data-theme') === 'dark' ? 'light' : 'dark');
        });
    }

    /*
     * Navigation on narrow viewports. Below 1000px the rail is out of flow, so
     * without this there is no way to reach another page: .mg-app is
     * overflow:hidden and only .mg-content scrolls.
     */
    const app = document.querySelector('.mg-app');
    const navToggle = document.getElementById('mg-topbar-menu');
    if (app && navToggle) {
        const setOpen = open => {
            app.classList.toggle('is-navopen', open);
            navToggle.setAttribute('aria-expanded', String(open));
        };
        navToggle.addEventListener('click', () => {
            setOpen(!app.classList.contains('is-navopen'));
        });
        // The scrim is a pseudo-element on .mg-app, so its clicks land here.
        app.addEventListener('click', e => {
            if (app.classList.contains('is-navopen') &&
                !e.target.closest('.mg-rail') &&
                !e.target.closest('#mg-topbar-menu')) {
                setOpen(false);
            }
        });
        document.addEventListener('keydown', e => {
            if (e.key === 'Escape') {
                setOpen(false);
            }
        });
    }

    // Marks the current page in the rail, so no page has to hardcode it.
    (function markActiveNav() {
        const here = location.pathname.split('/').pop() || 'index.html';
        document.querySelectorAll('.mg-nav a').forEach(a => {
            const href = (a.getAttribute('href') || '').split('/').pop();
            a.classList.toggle('is-active', href === here);
        });
    }());

    // Apply saved theme preference immediately
    setTheme(savedTheme);

    // 2. Pre-populate all chart containers with a stylized loading spinner
    document.querySelectorAll('.chart-container').forEach(el => {
        el.innerHTML = `
            <div class="d-flex flex-column align-items-center justify-content-center h-100 text-muted" style="min-height: 200px;">
                <svg class="icon icon-2x icon-spin mb-3" aria-hidden="true" style="color: #ff5c35;"><use href="#i-refresh"></use></svg>
                <span class="small font-weight-bold">Awaiting MoniGo server connection...</span>
            </div>
        `;
    });

    const elements = {
        healthMessage: document.getElementById('health-message'),
    };

    Object.values(elements).forEach(el => el && (el.innerHTML = refreshHtml));

    function fetchMetrics() {
        authenticatedFetch(`/monigo/api/v1/metrics`)
            .then(response => response.json())
            .then(data => {
                const { health } = data;
                const healthIndicator = document.getElementById('health-indicator');
                if (healthIndicator) {
                    if (health.service_health.healthy) {
                        healthIndicator.classList.remove('unhealthy');
                        healthIndicator.classList.add('healthy');
                    } else {
                        healthIndicator.classList.remove('healthy');
                        healthIndicator.classList.add('unhealthy');
                    }
                }
                const healthMsgEl = document.getElementById('health-message');
                if (healthMsgEl) {
                    healthMsgEl.textContent = health.service_health.message;
                }
            })
            .catch(error => {
                console.error('Error fetching metrics:', error);
            });
    }

    // --- Shell: service identity, nav badges, storage footer ---------------
    //
    // These live in the sidebar chrome because they are true of the whole
    // instrument rather than of any one page, so every page shows them and no
    // page has to fetch them twice.

    function setStatus(state) {
        const dot = document.getElementById('mg-status-dot');
        if (!dot) {
            return;
        }
        dot.classList.remove('is-stale', 'is-down');
        if (state !== 'ok') {
            dot.classList.add(state === 'down' ? 'is-down' : 'is-stale');
        }
        dot.title = state === 'ok' ? 'Connected'
            : state === 'down' ? 'Lost connection to the service'
            : 'Data is stale';
    }

    function fillShell() {
        authenticatedFetch('/monigo/api/v1/service-info')
            .then(r => {
                if (!r.ok) {
                    throw new Error('HTTP ' + r.status);
                }
                return r.json();
            })
            .then(info => {
                const name = info.service_name || 'service';
                const nameEl = document.getElementById('mg-service-name');
                const metaEl = document.getElementById('mg-service-meta');
                const avatar = document.getElementById('mg-avatar');
                if (nameEl) {
                    nameEl.textContent = name;
                    nameEl.title = name;
                }
                if (avatar) {
                    avatar.textContent = name.slice(0, 2);
                }
                if (metaEl) {
                    // The design's `pid 94917 · go1.21`: the two facts you need
                    // to be sure you are looking at the right process.
                    const go = (info.go_version || '').replace(/^go/, '');
                    metaEl.textContent = `pid ${info.process_id || '-'}${go ? ' \u00b7 go' + go : ''}`;
                }
                const version = document.getElementById('mg-version');
                if (version) {
                    // Read from build info server-side, and omitted when it is
                    // not knowable -- a local checkout or a replace directive.
                    // Hidden rather than shown as "(devel)".
                    version.textContent = info.monigo_version || '';
                    version.hidden = !info.monigo_version;
                }

                // Retention and footprint are properties of the instrument, so
                // they sit in the chrome. Both are omitted by the API when
                // unset, so absent means "not configured", not "zero".
                const mode = document.getElementById('mg-storage-mode');
                const store = document.getElementById('mg-storage-value');
                if (mode) {
                    const parts = [info.storage_type, info.retention_period].filter(Boolean);
                    mode.textContent = parts.join(' \u00b7 ');
                }
                if (store) {
                    store.textContent = info.storage_on_disk
                        ? `${info.storage_on_disk} retained`
                        : (info.storage_type === 'memory' ? 'in memory' : '');
                }
                setStatus('ok');
            })
            .catch(() => setStatus('down'));
    }

    function fillNavBadges() {
        authenticatedFetch('/monigo/api/v1/metrics')
            .then(r => r.json())
            .then(data => {
                const g = document.getElementById('mg-count-goroutines');
                if (g && data.core_statistics) {
                    g.textContent = data.core_statistics.goroutines;
                }

                // Traced-function count. The endpoint returns a map keyed by
                // function name, so its size is the count. Left blank when
                // nothing is traced yet -- an empty badge is hidden by CSS
                // rather than showing a zero that reads like a measurement.
                authenticatedFetch('/monigo/api/v1/function')
                    .then(r => r.json())
                    .then(fns => {
                        const el = document.getElementById('mg-count-functions');
                        const n = fns && typeof fns === 'object' ? Object.keys(fns).length : 0;
                        if (el) {
                            el.textContent = n > 0 ? String(n) : '';
                        }
                    })
                    .catch(() => { /* badge simply stays empty */ });
                // storage footer is filled from /service-info, not here
            })
            .catch(() => { /* the status dot already carries the failure */ });
    }

    fillShell();

    /* ================================================================ MG
     *
     * Shared runtime. Everything below is chrome that every page needs and no
     * page should re-implement: connection state, the as-of stamp, the
     * staleness banner, and the poll loop that drives them.
     *
     * Exposed on window.MG so the page renderers can drive it without each
     * keeping its own setInterval and its own idea of whether the server is
     * answering.
     */

    const status = { state: 'ok', fails: 0, lastGood: null };

    function clockUTC(d) {
        return (d || new Date()).toISOString().slice(11, 19) + 'Z';
    }

    function renderStatus() {
        const dot = document.getElementById('mg-status-dot');
        if (dot) {
            dot.classList.remove('is-stale', 'is-down');
            if (status.state !== 'ok') {
                dot.classList.add(status.state === 'down' ? 'is-down' : 'is-stale');
            }
            dot.title = status.state === 'ok' ? 'Connected'
                : status.state === 'down' ? 'Lost connection to the service'
                    : 'Data is stale';
        }

        // Dim the numbers while a poll is failing, so a stale reading cannot be
        // mistaken for a current one.
        const content = document.getElementById('mg-content');
        if (content) {
            content.classList.toggle('is-stale', status.state !== 'ok');
        }

        const banner = document.getElementById('mg-banner');
        if (!banner) {
            return;
        }
        if (status.state === 'ok') {
            banner.textContent = '';
            banner.hidden = true;
            return;
        }
        banner.hidden = false;
        banner.className = 'mg-note ' + (status.state === 'down' ? 'mg-note--bad' : 'mg-note--warn');
        const since = status.lastGood ? clockUTC(status.lastGood) : 'never';
        banner.innerHTML =
            '<svg class="mg-icon" aria-hidden="true"><use href="#i-alert-triangle"></use></svg>' +
            '<span><b>' +
            (status.state === 'down' ? 'No response from the service.' : 'Data is stale.') +
            '</b> Showing the last snapshot from ' + since + '. ' +
            (status.fails > 1 ? status.fails + ' failed polls.' : 'Retrying…') +
            '</span><button type="button" class="mg-note__act" id="mg-retry">Retry now</button>';
        const retry = document.getElementById('mg-retry');
        if (retry) {
            retry.addEventListener('click', () => Poll.tick(true));
        }
    }

    function markOk() {
        status.fails = 0;
        status.lastGood = new Date();
        if (status.state !== 'ok') {
            status.state = 'ok';
        }
        renderStatus();
        const el = document.getElementById('mg-asof');
        if (el) {
            el.textContent = 'as of ' + clockUTC(status.lastGood);
        }
    }

    function markFail(err) {
        status.fails++;
        // One miss is a blip. Escalating to "down" only after a retry has also
        // failed keeps a single dropped poll from flashing an alarm.
        status.state = status.fails >= 2 ? 'down' : 'stale';
        renderStatus();
        if (err) {
            console.error('[monigo] poll failed:', err);
        }
    }

    const Poll = {
        fn: null,
        interval: 15000,
        timer: null,
        running: true,

        start(fn, interval) {
            this.fn = fn;
            this.interval = interval || 15000;
            this.tick(true);
            this.timer = setInterval(() => Poll.tick(false), this.interval);

            const btn = document.getElementById('mg-live');
            if (btn) {
                btn.addEventListener('click', () => {
                    Poll.running = !Poll.running;
                    btn.classList.toggle('is-live', Poll.running);
                    btn.setAttribute('aria-pressed', String(Poll.running));
                    const label = document.getElementById('mg-live-label');
                    if (label) {
                        label.textContent = Poll.running ? 'LIVE' : 'PAUSED';
                    }
                    if (Poll.running) {
                        Poll.tick(true);
                    }
                });
            }

            // Polling a hidden tab spends the profiled process's CPU on nothing.
            document.addEventListener('visibilitychange', () => {
                if (!document.hidden && Poll.running) {
                    Poll.tick(false);
                }
            });
        },

        tick(force) {
            if (!this.fn) {
                return;
            }
            if (!force && (!this.running || document.hidden)) {
                return;
            }
            Promise.resolve()
                .then(() => this.fn())
                .then(() => markOk())
                .catch(err => markFail(err));
        },
    };

    // Inline SVG sparkline. Values are normalised across their own range, so
    // the shape is readable whatever the units.
    function sparkline(values, opts) {
        opts = opts || {};
        const w = opts.width || 240;
        const h = opts.height || 44;
        const stroke = opts.stroke || 'var(--acc)';
        const pts = (values || []).filter(v => typeof v === 'number' && isFinite(v));
        if (pts.length < 2) {
            return '<svg class="mg-spark" viewBox="0 0 ' + w + ' ' + h + '" preserveAspectRatio="none"></svg>';
        }
        const lo = Math.min(...pts);
        const hi = Math.max(...pts);
        const span = (hi - lo) || 1;
        const d = pts.map((v, i) => {
            const x = (i / (pts.length - 1)) * w;
            const y = h - 4 - ((v - lo) / span) * (h - 8);
            return (i ? 'L' : 'M') + x.toFixed(1) + ' ' + y.toFixed(1);
        }).join(' ');
        return '<svg class="mg-spark" viewBox="0 0 ' + w + ' ' + h + '" preserveAspectRatio="none">' +
            '<path d="' + d + ' L' + w + ' ' + h + ' L0 ' + h + ' Z" fill="' + stroke + '" opacity=".14"></path>' +
            '<path d="' + d + '" fill="none" stroke="' + stroke + '" stroke-width="1.6" ' +
            'vector-effect="non-scaling-stroke"></path></svg>';
    }

    window.MG = {
        Poll,
        status,
        sparkline,
        clockUTC,
        markOk,
        markFail,
        authenticatedFetch,
    };

    fillNavBadges();

    // Fetch metrics on load
    fetchMetrics();
});