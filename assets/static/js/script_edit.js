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

// 読みの確認。合成したらどう読まれるかを、合成する前に行ごとに見せます。
//
// 読みは自明ではありません（「水面」は ミナモ ではなく スイメン、算用数字も文脈で変わります）。
// 台本ぶんの合成時間を使ってから気付くと、その時間がそのまま無駄になります。返すのは
// サーバーで、合成の直前と同じ変換を通すため、ここに出るものが実際に読まれるものです。
//
// **表の中身をそのまま送ります。**保存済みの台本ではありません。直した行の読みを
// 確かめたいのに、保存しないと確かめられないのでは、直す前に戻ってしまいます。
(function () {
    'use strict';

    // 上限はサーバーと同じ 200 行です。ここで数えるのは、超えた分を送ってから
    // 400 で返されるより、押した時点で理由を出すほうが分かるためです。
    var MAX_LINES = 200;

    // texts は表の各行の本文を、行の順序のまま返します。
    // 空の行も落としません。応答は要求と同じ並びで返るので、番号がずれます。
    function texts(fields) {
        return fields.map(function (field) {
            return {text: field.value};
        });
    }

    // csrfToken は編集フォームに埋まっているトークンを読みます。
    // 画面が持っているものをそのまま使うので、別の口を用意する必要がありません。
    function csrfToken() {
        var input = document.querySelector('#script-form input[name="csrf_token"]');
        return input ? input.value : '';
    }

    // show は、返ってきた読みを各行の下へ差し込みます。
    function show(fields, lines) {
        fields.forEach(function (field, index) {
            var target = field.parentNode.querySelector('.js-reading');
            var line = lines[index];
            if (!target || !line) {
                return;
            }
            target.textContent = line.reading;
        });
    }

    // clear は差し込んだ読みを消します。送り直すたびに前回の結果が残らないようにします。
    function clear() {
        document.querySelectorAll('.js-reading').forEach(function (node) {
            node.textContent = '';
        });
    }

    // message は、応答の {"error": "..."} を取り出します。読めない応答でも
    // 画面には何か出します（黙って何も起きないのがいちばん困ります）。
    function message(response) {
        return response.json().then(function (body) {
            return (body && body.error) || '読みの取得に失敗しました';
        }, function () {
            return '読みの取得に失敗しました（' + response.status + '）';
        });
    }

    function preview(button, status) {
        var fields = Array.prototype.slice.call(document.querySelectorAll('#script-form .js-text'));
        if (fields.length === 0) {
            return;
        }
        if (fields.length > MAX_LINES) {
            status.textContent = '行が多すぎます（' + fields.length + ' 行、上限 ' + MAX_LINES + ' 行）';
            return;
        }

        clear();
        button.disabled = true;
        status.textContent = '確認しています…';

        fetch(button.dataset.endpoint, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'Accept': 'application/json',
                'X-CSRF-Token': csrfToken()
            },
            body: JSON.stringify({lines: texts(fields)})
        }).then(function (response) {
            if (!response.ok) {
                return message(response).then(function (text) {
                    throw new Error(text);
                });
            }
            return response.json();
        }).then(function (body) {
            show(fields, body.lines || []);
            status.textContent = '合成時にはこう読まれます。違う語はカタカナで書き直してください。';
        }).catch(function (error) {
            status.textContent = error.message;
        }).finally(function () {
            button.disabled = false;
        });
    }

    document.addEventListener('DOMContentLoaded', function () {
        var button = document.querySelector('.js-preview-reading');
        var status = document.querySelector('.js-reading-status');
        if (!button || !status) {
            return;
        }
        button.addEventListener('click', function () {
            preview(button, status);
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
