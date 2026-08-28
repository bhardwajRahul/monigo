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
     * The stylesheet keys off body[data-theme]. Dark is the default -- :root
     * carries the dark palette and light is the opt-in override -- so a first
     * visit with nothing stored lands on dark.
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

    let savedTheme = 'dark';
    try {
        savedTheme = localStorage.getItem('monigo-theme') || 'dark';
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
    fillNavBadges();

    // Fetch metrics on load
    fetchMetrics();
});