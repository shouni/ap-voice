// 台本の編集画面の振る舞いです。行の編集（スタイルの選択肢・追加・並べ替え・削除）と、
// 読みの確認、送信前の確認をまとめています。
//
// 以下はどれも、画面側に一覧や規則を写さないために書かれています。話者と
// スタイルの対応表はサーバーが data 属性で渡し（#voice-styles）、行数の上限も
// サーバーが渡します（#script-form の data-max-lines）。写すと必ずずれます。

// スタイルの選択肢を話者に合わせて差し替えます。
//
// 話者ごとに持つスタイルは違います（春日部つむぎは「ノーマル」だけ、
// ずんだもんは 8 種類）。全部を一律に並べると、その話者が持たない組み合わせを
// 選べてしまい、合成時に既定スタイルへ黙って落ちて指示が無視されます。
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

    // 上限はサーバーが持つ数を読みます（#script-form の data-max-lines）。
    // 超えた台本は保存時に弾かれ、そのとき画面は保存済みの台本を読み直すので
    // 編集中の内容が消えます。足せなくすることで、その経路を塞ぎます。
    //
    // 数をここへ写しません。写すと、どちらかを直したときにもう一方が古いまま
    // 残ります。読めなければ上限なしとして扱い、判断をサーバーへ返します。
    function maxLines() {
        var form = document.getElementById('script-form');
        var value = form ? parseInt(form.dataset.maxLines, 10) : NaN;
        return value > 0 ? value : Infinity;
    }

    // rowsOf は台本の行（tbody の tr）を並び順のまま返します。
    function rowsOf(body) {
        return Array.prototype.slice.call(body.querySelectorAll('tr'));
    }

    // refresh は、行を足し引きしたあとの見出しの行数を合わせます。
    function refresh(body) {
        var count = rowsOf(body).length;
        document.querySelectorAll('.js-line-count').forEach(function (node) {
            node.textContent = String(count);
        });
    }

    // fillRow は、1 行のスタイル選択肢を話者ぶんへ広げます。
    function fillRow(row) {
        var speakerSelect = row.querySelector('.js-speaker');
        var styleSelect = row.querySelector('.js-style');
        if (speakerSelect && styleSelect) {
            fillStyles(speakerSelect, styleSelect);
        }
    }

    // addAfter は、指定の行の下に空の行を差し込みます。
    //
    // 行はテンプレートから組み立てず、元の行を写して作ります。話者の選択肢も
    // スタイルの初期値も画面に既にあるので、同じ並びを JS 側へ書き写さずに済みます
    // （書き写すと assets/speakers.json と二重になります）。
    function addAfter(body, row) {
        if (rowsOf(body).length >= maxLines()) {
            return null;
        }
        var added = row.cloneNode(true);
        var text = added.querySelector('.js-text');
        if (text) {
            text.value = '';
        }
        var reading = added.querySelector('.js-reading');
        if (reading) {
            reading.textContent = '';
        }
        row.parentNode.insertBefore(added, row.nextSibling);
        fillRow(added);
        refresh(body);
        if (text) {
            text.focus();
        }
        return added;
    }

    // move は、行を 1 つ上（または下）へ入れ替えます。話す順序は台本そのものなので、
    // 入れ替えるのに本文を切り貼りさせる理由がありません。
    function move(row, up) {
        var sibling = up ? row.previousElementSibling : row.nextElementSibling;
        if (!sibling) {
            return;
        }
        if (up) {
            row.parentNode.insertBefore(row, sibling);
        } else {
            row.parentNode.insertBefore(sibling, row);
        }
    }

    // remove は行ごと取り除きます。話者・スタイル・本文は 3 つの並びとして送るので、
    // 行ごと消せば数が揃ったままです（本文だけ空にする従来の消し方も残ります）。
    function remove(body, row) {
        if (rowsOf(body).length <= 1) {
            // 最後の 1 行は消しません。空の台本は保存できず、行が 0 だと
            // 写して増やす元も無くなります。
            var text = row.querySelector('.js-text');
            if (text) {
                text.value = '';
                text.focus();
            }
            return;
        }
        row.remove();
        refresh(body);
    }

    document.addEventListener('DOMContentLoaded', function () {
        var body = document.querySelector('#script-form tbody');
        if (!body) {
            return;
        }

        // 行は増えるので、行ごとではなく表に 1 つだけ張ります。写した行にも
        // そのまま効くため、複製のたびにイベントを張り直す必要がありません。
        body.addEventListener('change', function (event) {
            var row = event.target.closest('tr');
            if (row && event.target.classList.contains('js-speaker')) {
                fillRow(row);
            }
        });

        body.addEventListener('click', function (event) {
            var button = event.target.closest('button');
            var row = event.target.closest('tr');
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

        // 初回。保存済みの値 1 つだけが入っている状態から、話者ぶんへ広げます。
        rowsOf(body).forEach(fillRow);
        refresh(body);
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

    // 上限はサーバーが持つ数を読みます（#script-form の data-max-lines）。
    // ここで数えるのは、超えた分を送ってから 400 で返されるより、押した時点で
    // 理由を出すほうが分かるためです。読めなければ送って判断を委ねます。
    function maxLines() {
        var form = document.getElementById('script-form');
        var value = form ? parseInt(form.dataset.maxLines, 10) : NaN;
        return value > 0 ? value : Infinity;
    }

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
        var limit = maxLines();
        if (fields.length > limit) {
            status.textContent = '行が多すぎます（' + fields.length + ' 行、上限 ' + limit + ' 行）';
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
