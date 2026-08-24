// 台本の編集画面で、スタイルの選択肢を話者に合わせて差し替えます。
//
// 話者ごとに持つスタイルは違います（春日部つむぎは「ノーマル」だけ、
// ずんだもんは 8 種類）。全部を一律に並べると、その話者が持たない組み合わせを
// 選べてしまい、合成時に既定スタイルへ黙って落ちて指示が無視されます。
//
// 対応表はサーバーが埋め込みます（#voice-styles の data-styles）。**画面側は一覧を持ちません。**
// 話者一覧は assets/speakers.json が唯一の出所で、ここに写すと必ずずれます。
(function () {
    'use strict';

    // 属性から読み戻します。インラインスクリプトで window へ置く形はやめました
    // （CSP の script-src を 'self' だけにするため）。壊れた値で画面全体を止めない
    // よう、解析に失敗したら選択肢の差し替えだけを諦めます。
    var stylesBySpeaker = {};
    var stylesHolder = document.getElementById('voice-styles');
    if (stylesHolder) {
        try {
            stylesBySpeaker = JSON.parse(stylesHolder.dataset.styles || '{}');
        } catch (error) {
            console.warn('話者スタイルの対応表を読めませんでした:', error);
        }
    }

    // fillStyles は、選ばれている話者のスタイルで select を組み直します。
    // 直前に選ばれていた値が新しい話者にもあれば、それを保ちます。
    function fillStyles(speakerSelect, styleSelect) {
        var styles = stylesBySpeaker[speakerSelect.value] || [];
        if (styles.length === 0) {
            return;
        }

        var wanted = styleSelect.value || styleSelect.getAttribute('data-selected');
        styleSelect.textContent = '';

        styles.forEach(function (name) {
            var option = document.createElement('option');
            option.value = name;
            option.textContent = name;
            if (name === wanted) {
                option.selected = true;
            }
            styleSelect.appendChild(option);
        });

        // 前の話者のスタイルが新しい話者に無ければ、先頭（その話者の既定）へ落とします。
        if (styles.indexOf(wanted) === -1) {
            styleSelect.selectedIndex = 0;
        }
    }

    document.addEventListener('DOMContentLoaded', function () {
        document.querySelectorAll('tr').forEach(function (row) {
            var speakerSelect = row.querySelector('.js-speaker');
            var styleSelect = row.querySelector('.js-style');
            if (!speakerSelect || !styleSelect) {
                return;
            }
            speakerSelect.addEventListener('change', function () {
                fillStyles(speakerSelect, styleSelect);
            });
            // 初回。保存済みの値 1 つだけが入っている状態から、話者ぶんへ広げます。
            fillStyles(speakerSelect, styleSelect);
        });
    });
})();

// 送信前の確認は data-confirm で宣言します。onsubmit 属性のままだと CSP の
// script-src に 'unsafe-inline' が必要になり、インラインスクリプト禁止が無意味になります。
document.addEventListener('submit', function (event) {
    var form = event.target.closest('form[data-confirm]');
    if (form && !window.confirm(form.dataset.confirm)) {
        event.preventDefault();
    }
});
