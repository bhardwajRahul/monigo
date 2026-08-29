/*
 * Exporters page renderer.
 *
 * Push and pull exporters are shown with different facts, because they do not
 * mean the same things. MoniGo pushes to OTel, so last-attempt, consecutive
 * failures and the last error are all meaningful there. Prometheus scrapes
 * MoniGo, so "last export succeeded" is meaningless: the honest reading is
 * when it was last scraped, and there is no failure count at all -- a scrape
 * that fails, fails at the client and the server never learns of it. Showing
 * "0 failures" for Prometheus would assert a health signal that does not
 * exist.
 */
document.addEventListener('DOMContentLoaded', () => {
    'use strict';

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

    function esc(s) {
        return String(s == null ? '' : s).replace(/[&<>"']/g, c => ({
            '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'
        }[c]));
    }

    /* Relative time. Anything older than a day is given as a date: "31d ago"
       reads as a measurement, and at that age it is really just "stale". */
    function ago(iso) {
        if (!iso) { return 'never'; }
        const then = new Date(iso);
        if (isNaN(then)) { return 'unknown'; }
        const secs = Math.floor((Date.now() - then.getTime()) / 1000);
        if (secs < 0) { return 'just now'; }
        if (secs < 60) { return secs + 's ago'; }
        if (secs < 3600) { return Math.floor(secs / 60) + 'm ago'; }
        if (secs < 86400) { return Math.floor(secs / 3600) + 'h ago'; }
        return then.toISOString().slice(0, 10);
    }

    const STATE_TONE = {
        ok: 'is-ok',
        retrying: 'is-warn',
        failing: 'is-bad',
        never: 'is-unknown'
    };

    /* The word on the pill. "never" is spelled out per kind, because "never"
       alone reads as a fault, and for a pull exporter that nobody has scraped
       yet it is simply the starting state. */
    function stateLabel(e) {
        if (e.state !== 'never') { return e.state; }
        return e.kind === 'pull' ? 'not scraped' : 'idle';
    }

    function fact(label, value, bad) {
        return '<div class="mg-exp-fact">' +
            '<span class="mg-exp-factlabel">' + esc(label) + '</span>' +
            '<span class="mg-exp-factvalue' + (bad ? ' is-bad' : '') + '">' +
            esc(value) + '</span></div>';
    }

    function card(e) {
        const tone = STATE_TONE[e.state] || 'is-unknown';
        let facts = '';

        if (e.kind === 'pull') {
            facts += fact('Last scrape', ago(e.last_success));
            facts += fact('Scrapes', e.total || 0);
        } else {
            facts += fact('Last success', ago(e.last_success));
            facts += fact('Last attempt', ago(e.last_attempt));
            // "Attempts", not "Exports": total counts every try, so a card
            // reading "Exports 1 / Failures 1" would otherwise claim one
            // export happened when none did.
            facts += fact('Attempts', e.total || 0);
            facts += fact('Failures', e.failures || 0, (e.failures || 0) > 0);
            if ((e.consecutive_failures || 0) > 0) {
                facts += fact('Consecutive', e.consecutive_failures, true);
            }
        }

        let note = '';
        if (e.kind === 'pull') {
            note = '<span class="mg-exp-note">Prometheus scrapes MoniGo at ' +
                '<code>/metrics</code>. A failed scrape fails at the collector, ' +
                'so no error is visible from here.</span>';
        } else if (e.state === 'never') {
            note = '<span class="mg-exp-note">Configured, nothing sent yet. ' +
                'The first export runs at startup, then once per interval.</span>';
        }

        return '<article class="mg-card mg-exp-card">' +
            '<div class="mg-exp-head">' +
            '<span class="mg-exp-name">' + esc(e.name) + '</span>' +
            '<span class="mg-exp-kind">' + esc(e.kind) + '</span>' +
            '<span class="mg-exp-state ' + tone + '">' + esc(stateLabel(e)) + '</span>' +
            '</div>' +
            '<div class="mg-exp-facts">' + facts + '</div>' +
            (e.last_error ? '<div class="mg-exp-error">' + esc(e.last_error) + '</div>' : '') +
            note +
            '</article>';
    }

    const grid = document.getElementById('mg-exp-grid');
    const empty = document.getElementById('mg-exp-empty');

    function render(data) {
        const list = (data && data.exporters) || [];
        if (!list.length) {
            grid.innerHTML = '';
            empty.hidden = false;
            empty.textContent = 'No exporters reported.';
            return;
        }
        empty.hidden = true;
        grid.innerHTML = list.map(card).join('');

        /* Absence of a push exporter is a configuration fact, not a failure,
           and saying so is more useful than an empty area the reader has to
           interpret. */
        if (!list.some(e => e.kind === 'push')) {
            empty.hidden = false;
            empty.textContent =
                'No push exporter configured. Add one with WithOTelEndpoint("host:4317").';
        }
    }

    function load() {
        return authenticatedFetch('/monigo/api/v1/exporters')
            .then(r => {
                if (!r.ok) { throw new Error('HTTP ' + r.status); }
                return r.json();
            })
            .then(data => {
                render(data);
                if (window.MG && MG.markOk) { MG.markOk(); }
            })
            .catch(() => {
                if (window.MG && MG.markFail) { MG.markFail(); }
            });
    }

    if (window.MG && MG.Poll) {
        MG.Poll.start(load, 10000);
    } else {
        load();
        setInterval(load, 10000);
    }
});
