document.addEventListener('DOMContentLoaded', () => {
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

    const refreshHtml = `
        <div class="loader-container">
            <div class="bouncing-dots">
                <div class="dot"></div>
                <div class="dot"></div>
                <div class="dot"></div>
            </div>
        </div>`;


    function toLocalISOString(date) {
        const tzOffset = -date.getTimezoneOffset(); // in minutes
        const diff = tzOffset >= 0 ? '+' : '-';
        const pad = (num) => `${Math.floor(Math.abs(num))}`.padStart(2, '0');

        const offsetHours = pad(tzOffset / 60);
        const offsetMinutes = pad(tzOffset % 60);

        return date.getFullYear() +
            '-' + pad(date.getMonth() + 1) +
            '-' + pad(date.getDate()) +
            'T' + pad(date.getHours()) +
            ':' + pad(date.getMinutes()) +
            ':' + pad(date.getSeconds()) +
            '.' + String((date.getMilliseconds() / 1000).toFixed(3)).slice(2, 5) +
            diff + offsetHours + ':' + offsetMinutes;
    }


    function updateTable(metric, timeframe) {

        let StartTime = new Date();
        let EndTime = new Date();

        if (timeframe == "5m") {
            StartTime = new Date(new Date().getTime() - 5 * 60000); // Subtract 5 minutes
        } else if (timeframe == "10m") {
            StartTime = new Date(new Date().getTime() - 10 * 60000); // Subtract 10 minutes
        } else if (timeframe == "30m") {
            StartTime = new Date(new Date().getTime() - 30 * 60000); // Subtract 30 minutes
        } else if (timeframe == "1h") {
            StartTime = new Date(new Date().getTime() - 60 * 60000); // Subtract 1 hour
        } else if (timeframe == "6h") {
            StartTime = new Date(new Date().getTime() - 360 * 60000); // Subtract 6 hours
        } else if (timeframe == "1d") {
            StartTime = new Date(new Date().getTime() - 1440 * 60000); // Subtract 1 day
        } else if (timeframe == "3d") {
            StartTime = new Date(new Date().getTime() - 4320 * 60000); // Subtract 3 days
        } else if (timeRange == "7d") {
            StartTime = new Date(new Date().getTime() - 10080 * 60000); // Subtract 7 days
        }

        // else if (timeRange == "7d") {
        //     StartTime = new Date(new Date().getTime() - 10080 * 60000); // Subtract 7 days
        // } else if (timeRange == "1month") {
        //     StartTime = new Date(new Date().getTime() - 43200 * 60000); // Subtract 1 month
        // }

        let reqObj = {
            topic: metric,
            start_time: toLocalISOString(StartTime),
            end_time: toLocalISOString(EndTime),
            time_frame: timeframe
        };

        authenticatedFetch('/monigo/api/v1/reports', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json'
            },
            body: JSON.stringify(reqObj)
        })
            .then(response => response.json())
            .then(data => {
                const tablesContainer = document.getElementById('tablesContainer');

                if (data.length > 0) {
                    const table = createTable(topic, data);
                    tablesContainer.appendChild(table);
                    const downloadBtn = document.getElementById('downloadBtn');
                    if (downloadBtn) {
                        downloadBtn.style.display = 'block';
                        downloadBtn.addEventListener('click', () => downloadCSV(data, metric));
                    }
                } else {
                    tablesContainer.innerHTML = '';
                    const downloadBtn = document.getElementById('downloadBtn');
                    if (downloadBtn) {
                        downloadBtn.style.display = 'none';
                    }
                }
            }).catch((error) => {
                console.error('Error:', error);
            });
    }

    function downloadCSV(data, metric) {
        const directHeaders = data.length > 0 ? Object.keys(data[0]).filter(header => header !== 'value') : [];
        const valueHeaders = data.length > 0 && data[0].value ? Object.keys(data[0].value) : [];
        const headers = [...directHeaders, ...valueHeaders];

        // headers in toUpperCase
        const headersUpperCase = headers.map(header => header.replace(/_/g, ' ').toUpperCase());

        const csvContent = [
            headersUpperCase.join(','),
            ...data.map(item => {
                const directValues = directHeaders.map(header => item[header]);
                const valueValues = valueHeaders.map(header => item.value[header]);
                return [...directValues, ...valueValues].join(',');
            })
        ].join('\n');

        const blob = new Blob([csvContent], { type: 'text/csv' });
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = `${metric}.csv`;
        a.click();
        URL.revokeObjectURL(url);
    }


    function createTable(topic, data) {
        const table = document.createElement('div');
        table.classList.add('table-responsive', 'rounded', 'mb-3');
        const tableElement = document.createElement('table');
        tableElement.classList.add('data-table', 'table', 'mb-0', 'tbl-server-info');
        const thead = document.createElement('thead');
        const isDark = document.body.classList.contains('dark-theme');
        thead.classList.add(isDark ? 'bg-dark' : 'bg-white', 'text-uppercase');
        const tbody = document.createElement('tbody');
        tbody.classList.add('ligth-body');
        const directHeaders = data.length > 0 ? Object.keys(data[0]).filter(header => header !== 'value') : [];
        const valueHeaders = data.length > 0 && data[0].value ? Object.keys(data[0].value) : [];
        const headers = [...directHeaders, ...valueHeaders];

        const headerRow = document.createElement('tr');
        headerRow.classList.add('ligth', 'ligth-data');
        headers.forEach(header => {
            const th = document.createElement('th');
            th.textContent = header.replace(/_/g, ' ').toUpperCase();
            headerRow.appendChild(th);
        });
        thead.appendChild(headerRow);

        data.forEach(item => {
            const row = document.createElement('tr');
            headers.forEach(header => {
                const td = document.createElement('td');
                if (header in item) {
                    td.textContent = item[header];
                } else if (header in item.value) {
                    td.textContent = item.value[header];
                } else {
                    td.textContent = '';
                }
                row.appendChild(td);
            });
            tbody.appendChild(row);
        });

        tableElement.appendChild(thead);
        tableElement.appendChild(tbody);
        table.appendChild(tableElement);
        document.body.appendChild(table);
        return table;
    }

    // Function to update chart based on selections
    function updateTableCompo() {

        // const sectionTitle = document.getElementById('sectionTitle');
        const tablesContainer = document.getElementById('tablesContainer');
        // sectionTitle.textContent = 'Loading...';
        tablesContainer.innerHTML = '';
        const metricSelect = document.getElementById('topic').value;
        const timeSelect = document.getElementById('timeframe').value;
        updateTable(metricSelect, timeSelect);
    }

    document.getElementById('topic').addEventListener('change', updateTableCompo);
    document.getElementById('timeframe').addEventListener('change', updateTableCompo);
    updateTableCompo();
});