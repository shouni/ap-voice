// 生成モードの説明を、選択に合わせて差し替えます。
//
// 説明そのものはプロンプト（prompts/<mode>.md）の front matter が持っていて、
// option の data 属性で運ばれてきます。ここは表示だけを担当するので、
// モードを足しても JS は触りません。
(function () {
    'use strict';

    // appendLine は空でない説明だけを 1 行として足します。
    function appendLine(target, text, className) {
        if (!text) {
            return;
        }
        var line = document.createElement('div');
        if (className) {
            line.className = className;
        }
        // front matter は書き手のものですが、HTML として解釈させる理由はありません。
        line.textContent = text;
        target.appendChild(line);
    }

    function render(select, target) {
        target.textContent = '';

        var option = select.options[select.selectedIndex];
        if (!option) {
            return;
        }
        appendLine(target, option.getAttribute('data-direction'), '');

        var useWhen = option.getAttribute('data-usewhen');
        if (useWhen) {
            appendLine(target, '向いている: ' + useWhen, 'text-muted');
        }
    }

    document.addEventListener('DOMContentLoaded', function () {
        document.querySelectorAll('.js-mode-desc[data-for]').forEach(function (target) {
            var select = document.getElementById(target.getAttribute('data-for'));
            if (!select) {
                return;
            }
            select.addEventListener('change', function () {
                render(select, target);
            });
            // 初回描画。選択済みの状態（投入後の再描画を含む）に合わせます。
            render(select, target);
        });
    });
})();
