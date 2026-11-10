// Swaps the generation mode's description to match the selection.
//
// The description itself lives in the front matter of the prompt (prompts/<mode>.md) and
// arrives through the option's data attributes. This only handles display, so
// adding a mode does not touch the JS.
(() => {
    'use strict';

    // appendLine adds a non-empty description as one line.
    const appendLine = (target, text, className) => {
        if (!text) {
            return;
        }
        const line = document.createElement('div');
        if (className) {
            line.className = className;
        }
        // The front matter belongs to its author, but there is no reason to have it interpreted as HTML.
        line.textContent = text;
        target.appendChild(line);
    };

    const render = (select, target) => {
        target.textContent = '';

        const option = select.options[select.selectedIndex];
        if (!option) {
            return;
        }
        appendLine(target, option.dataset.direction, '');

        const useWhen = option.dataset.usewhen;
        if (useWhen) {
            appendLine(target, `向いている: ${useWhen}`, 'text-muted');
        }
    };

    document.addEventListener('DOMContentLoaded', () => {
        for (const target of document.querySelectorAll('.js-mode-desc[data-for]')) {
            const select = document.getElementById(target.dataset.for);
            if (!select) {
                continue;
            }
            select.addEventListener('change', () => render(select, target));
            // First render. Matches the selected state (including the re-render after submission).
            render(select, target);
        }
    });
})();
