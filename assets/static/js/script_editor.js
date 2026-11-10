// Behaviour of the script editor. It gathers line editing (style options, adding, reordering,
// deleting) and reading checks.
//
// Both are written so that no list or rule is copied into the page. The speaker-to-style table
// comes from the server in a data attribute (#voice-styles), and so does the line cap
// (data-max-lines on #script-form). Copies always drift.
(() => {
    'use strict';

    // Read back from the attribute. Placing it on window with an inline script was dropped
    // (to keep the CSP's script-src at 'self'). So that a broken value does not stop the whole page,
    // a parse failure only gives up on swapping the options.
    const stylesBySpeaker = (() => {
        const holder = document.getElementById('voice-styles');
        if (!holder) {
            return {};
        }
        try {
            return JSON.parse(holder.dataset.styles || '{}');
        } catch (error) {
            console.warn('話者スタイルの対応表を読めませんでした:', error);
            return {};
        }
    })();

    // maxLines is the line cap. It only reads the number the server holds and does not copy it here
    // (a copy would stay stale when the other is fixed). If unreadable, it is
    // treated as no cap, and the decision goes back to the server.
    //
    // A script over the cap is rejected on save, and the page then reloads the stored script,
    // losing what was being edited. Refusing to add more closes that path.
    const maxLines = () => {
        const form = document.getElementById('script-form');
        const value = form ? parseInt(form.dataset.maxLines, 10) : NaN;
        return value > 0 ? value : Infinity;
    };

    // --- Line editing ----------------------------------------------------------

    // fillStyles rebuilds the select with the selected speaker's styles.
    // If the previously selected value exists for the new speaker too, it is kept.
    //
    // Each speaker has different styles (春日部つむぎ has only 「ノーマル」, ずんだもん has
    // 8). Listing all of them for everyone would allow pairs the speaker does not have, which at
    // synthesis silently fall back to the default style, ignoring the instruction.
    const fillStyles = (speakerSelect, styleSelect) => {
        const styles = stylesBySpeaker[speakerSelect.value] || [];
        if (styles.length === 0) {
            return;
        }

        const wanted = styleSelect.value || styleSelect.dataset.selected;
        styleSelect.textContent = '';

        for (const name of styles) {
            const option = document.createElement('option');
            option.value = name;
            option.textContent = name;
            option.selected = name === wanted;
            styleSelect.appendChild(option);
        }

        // If the previous speaker's style does not exist for the new one, fall back to the first (that speaker's default).
        if (!styles.includes(wanted)) {
            styleSelect.selectedIndex = 0;
        }
    };

    // rowsOf returns the script's rows (the tbody's tr) in order.
    const rowsOf = (body) => Array.from(body.querySelectorAll('tr'));

    // refresh updates the line count in the heading after rows are added or removed.
    const refresh = (body) => {
        const count = rowsOf(body).length;
        for (const node of document.querySelectorAll('.js-line-count')) {
            node.textContent = String(count);
        }
    };

    // fillRow expands one row's style options to the speaker's set.
    const fillRow = (row) => {
        const speakerSelect = row.querySelector('.js-speaker');
        const styleSelect = row.querySelector('.js-style');
        if (speakerSelect && styleSelect) {
            fillStyles(speakerSelect, styleSelect);
        }
    };

    // addAfter inserts an empty row below the given one.
    //
    // Rows are not built from a template but copied from an existing row. The speaker options and
    // the initial style are already on the page, so the same list need not be copied into JS
    // (a copy would duplicate assets/speakers.json).
    const addAfter = (body, row) => {
        if (rowsOf(body).length >= maxLines()) {
            return;
        }

        const added = row.cloneNode(true);
        const text = added.querySelector('.js-text');
        if (text) {
            text.value = '';
        }
        const reading = added.querySelector('.js-reading');
        if (reading) {
            reading.textContent = '';
        }

        row.after(added);
        fillRow(added);
        refresh(body);
        text?.focus();
    };

    // move swaps a row one up (or down). The speaking order is the script itself, so
    // there is no reason to make anyone cut and paste text to reorder.
    const move = (row, up) => {
        const sibling = up ? row.previousElementSibling : row.nextElementSibling;
        if (!sibling) {
            return;
        }
        if (up) {
            sibling.before(row);
        } else {
            sibling.after(row);
        }
    };

    // remove takes out the whole row. Speaker, style and text are sent as three lists,
    // so removing whole rows keeps their counts equal (the old way, emptying the text, still works).
    const remove = (body, row) => {
        if (rowsOf(body).length <= 1) {
            // The last row is not removed. An empty script cannot be saved, and with zero rows
            // there would be nothing to copy from to add more.
            const text = row.querySelector('.js-text');
            if (text) {
                text.value = '';
                text.focus();
            }
            return;
        }
        row.remove();
        refresh(body);
    };

    // --- Reading checks --------------------------------------------------------

    // Readings are not obvious (「水面」 reads スイメン, not ミナモ, and Arabic numerals change with
    // context). Noticing after spending the script's synthesis time wastes all of that
    // time. The server returns the readings through the same conversion as just before synthesis, so what
    // appears here is what will actually be read.
    //
    // It sends the table's contents as they are, not the stored script. Having to save before
    // checking the reading of a line you just fixed would defeat the purpose.

    // show inserts the returned readings below each row.
    const show = (fields, lines) => {
        fields.forEach((field, index) => {
            const target = field.parentNode.querySelector('.js-reading');
            const reading = lines[index]?.reading;
            if (target && reading !== undefined) {
                target.textContent = reading;
            }
        });
    };

    // clearReadings removes the inserted readings, so the previous result does not linger on each resend.
    const clearReadings = () => {
        for (const node of document.querySelectorAll('.js-reading')) {
            node.textContent = '';
        }
    };

    // failureMessage extracts {"error": "..."} from the response. Even for an unreadable response
    // something is shown (nothing happening silently is the worst).
    const failureMessage = async (response) => {
        try {
            const body = await response.json();
            return body?.error || '読みの取得に失敗しました';
        } catch {
            return `読みの取得に失敗しました（${response.status}）`;
        }
    };

    const preview = async (button, status) => {
        const fields = Array.from(document.querySelectorAll('#script-form .js-text'));
        if (fields.length === 0) {
            return;
        }
        // Showing the reason when the button is pressed is clearer than sending the excess and getting a 400 back.
        const limit = maxLines();
        if (fields.length > limit) {
            status.textContent = `行が多すぎます（${fields.length} 行、上限 ${limit} 行）`;
            return;
        }

        clearReadings();
        button.disabled = true;
        status.textContent = '確認しています…';

        try {
            const response = await fetch(button.dataset.endpoint, {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    'Accept': 'application/json',
                    'X-CSRF-Token': window.App.csrfToken()
                },
                // Empty rows are not dropped either. The response comes back in the request's order, so the numbering would shift.
                body: JSON.stringify({lines: fields.map((field) => ({text: field.value}))})
            });
            if (!response.ok) {
                throw new Error(await failureMessage(response));
            }
            const body = await response.json();
            show(fields, body.lines || []);
            status.textContent = '合成時にはこう読まれます。違う語はカタカナで書き直してください。';
        } catch (error) {
            status.textContent = error.message;
        } finally {
            button.disabled = false;
        }
    };

    // --- Wiring ----------------------------------------------------------------

    document.addEventListener('DOMContentLoaded', () => {
        const body = document.querySelector('#script-form tbody');
        if (!body) {
            return;
        }

        // Rows multiply, so one listener goes on the table rather than on each row. It also applies to
        // copied rows, so there is no need to re-bind events on every copy.
        body.addEventListener('change', (event) => {
            const row = event.target.closest('tr');
            if (row && event.target.classList.contains('js-speaker')) {
                fillRow(row);
            }
        });

        body.addEventListener('click', (event) => {
            const button = event.target.closest('button');
            const row = event.target.closest('tr');
            if (!button || !row) {
                return;
            }
            if (button.classList.contains('js-move-up')) {
                move(row, true);
            } else if (button.classList.contains('js-move-down')) {
                move(row, false);
            } else if (button.classList.contains('js-add-row')) {
                addAfter(body, row);
            } else if (button.classList.contains('js-remove-row')) {
                remove(body, row);
            }
        });

        // First render. From the state holding only the stored value, expand to the speaker's set.
        rowsOf(body).forEach(fillRow);
        refresh(body);

        const previewButton = document.querySelector('.js-preview-reading');
        const previewStatus = document.querySelector('.js-reading-status');
        if (previewButton && previewStatus) {
            previewButton.addEventListener('click', () => preview(previewButton, previewStatus));
        }
    });
})();
