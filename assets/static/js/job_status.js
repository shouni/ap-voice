// Watches a running job and reloads the page when its state changes.
//
// After submitting, this page just said "running", and whether it had finished was unknown
// until reloaded by hand. Synthesis takes minutes, so
// whoever pressed the button either waited or gave up and waited for the Slack notification.
//
// The server already returns the state (the JSON of GET /jobs/{id}). All this does is
// read it periodically and reload the whole page once the record moves. Building whether the
// script or audio exists on the page side would duplicate the server's rendering.
(() => {
    'use strict';

    // Synthesis takes minutes, so polling every second is pointless. The interval is fine enough
    // against one job's limit (PIPELINE_TIMEOUT, 25 minutes by default) and quiet.
    const INTERVAL_MS = 15000;

    // The limit before watching stops. It keeps a tab left open from polling forever.
    // It stops a little past the job limit (25 minutes).
    const MAX_POLLS = 120;

    // Finished states. Once it lands here, what this page shows is settled.
    const TERMINAL = ['succeeded', 'failed'];

    let polls = 0;

    const check = async (holder, timer) => {
        polls += 1;
        if (polls > MAX_POLLS) {
            window.clearInterval(timer);
            return;
        }

        try {
            const response = await fetch(holder.dataset.endpoint, {
                headers: {'Accept': 'application/json'},
                // The history's state is not left to the browser cache. Otherwise it would keep reading stale
                // responses while watching precisely to notice a change.
                cache: 'no-store'
            });
            if (!response.ok) {
                // A job with no record (404) will not grow a state by waiting.
                // Nor is there anything the page can fix when it is merely unreadable.
                window.clearInterval(timer);
                return;
            }

            const status = await response.json();
            if (status?.state && status.state !== holder.dataset.state) {
                // That much tells us it changed. What changed and how is seen in
                // what the server renders again.
                window.clearInterval(timer);
                window.location.reload();
            }
        } catch {
            // If it is only a network failure, the next cycle picks it up again. Even consecutive failures
            // stop at MAX_POLLS.
        }
    };

    document.addEventListener('DOMContentLoaded', () => {
        const holder = document.getElementById('job-state');
        if (!holder?.dataset.endpoint) {
            return;
        }
        // Finished jobs are not watched. It would only add a round trip on every open.
        if (!holder.dataset.state || TERMINAL.includes(holder.dataset.state)) {
            return;
        }

        const timer = window.setInterval(() => check(holder, timer), INTERVAL_MS);
    });
})();
