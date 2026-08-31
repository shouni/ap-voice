// 生成モードの説明を、選択に合わせて差し替えます。
//
// 説明そのものはプロンプト（prompts/<mode>.md）の front matter が持っていて、
// option の data 属性で運ばれてきます。ここは表示だけを担当するので、
// モードを足しても JS は触りません。
(() => {
    'use strict';

    // appendLine は空でない説明だけを 1 行として足します。
    const appendLine = (target, text, className) => {
        if (!text) {
            return;
        }
        const line = document.createElement('div');
        if (className) {
            line.className = className;
        }
        // front matter は書き手のものですが、HTML として解釈させる理由はありません。
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
            // 初回描画。選択済みの状態（投入後の再描画を含む）に合わせます。
            render(select, target);
        }
    });
})();
