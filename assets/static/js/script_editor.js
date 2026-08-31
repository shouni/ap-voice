// 台本の編集画面の振る舞いです。行の編集（スタイルの選択肢・追加・並べ替え・削除）と、
// 読みの確認をまとめています。
//
// どちらも、画面側に一覧や規則を写さないために書かれています。話者とスタイルの対応表は
// サーバーが data 属性で渡し（#voice-styles）、行数の上限もサーバーが渡します
// （#script-form の data-max-lines）。写すと必ずずれます。
(() => {
    'use strict';

    // 属性から読み戻します。インラインスクリプトで window へ置く形はやめました
    // （CSP の script-src を 'self' だけにするため）。壊れた値で画面全体を止めない
    // よう、解析に失敗したら選択肢の差し替えだけを諦めます。
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

    // maxLines は行数の上限です。サーバーが持つ数を読むだけで、ここへは写しません
    // （写すと、どちらかを直したときにもう一方が古いまま残ります）。読めなければ
    // 上限なしとして扱い、判断をサーバーへ返します。
    //
    // 超えた台本は保存時に弾かれ、そのとき画面は保存済みの台本を読み直すので、
    // 編集中の内容が消えます。足せなくすることで、その経路を塞ぎます。
    const maxLines = () => {
        const form = document.getElementById('script-form');
        const value = form ? parseInt(form.dataset.maxLines, 10) : NaN;
        return value > 0 ? value : Infinity;
    };

    // --- 行の編集 -----------------------------------------------------------

    // fillStyles は、選ばれている話者のスタイルで select を組み直します。
    // 直前に選ばれていた値が新しい話者にもあれば、それを保ちます。
    //
    // 話者ごとに持つスタイルは違います（春日部つむぎは「ノーマル」だけ、ずんだもんは
    // 8 種類）。全部を一律に並べると、その話者が持たない組み合わせを選べてしまい、
    // 合成時に既定スタイルへ黙って落ちて指示が無視されます。
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

        // 前の話者のスタイルが新しい話者に無ければ、先頭（その話者の既定）へ落とします。
        if (!styles.includes(wanted)) {
            styleSelect.selectedIndex = 0;
        }
    };

    // rowsOf は台本の行（tbody の tr）を並び順のまま返します。
    const rowsOf = (body) => Array.from(body.querySelectorAll('tr'));

    // refresh は、行を足し引きしたあとの見出しの行数を合わせます。
    const refresh = (body) => {
        const count = rowsOf(body).length;
        for (const node of document.querySelectorAll('.js-line-count')) {
            node.textContent = String(count);
        }
    };

    // fillRow は、1 行のスタイル選択肢を話者ぶんへ広げます。
    const fillRow = (row) => {
        const speakerSelect = row.querySelector('.js-speaker');
        const styleSelect = row.querySelector('.js-style');
        if (speakerSelect && styleSelect) {
            fillStyles(speakerSelect, styleSelect);
        }
    };

    // addAfter は、指定の行の下に空の行を差し込みます。
    //
    // 行はテンプレートから組み立てず、元の行を写して作ります。話者の選択肢も
    // スタイルの初期値も画面に既にあるので、同じ並びを JS 側へ書き写さずに済みます
    // （書き写すと assets/speakers.json と二重になります）。
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

    // move は、行を 1 つ上（または下）へ入れ替えます。話す順序は台本そのものなので、
    // 入れ替えるのに本文を切り貼りさせる理由がありません。
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

    // remove は行ごと取り除きます。話者・スタイル・本文は 3 つの並びとして送るので、
    // 行ごと消せば数が揃ったままです（本文だけ空にする従来の消し方も残ります）。
    const remove = (body, row) => {
        if (rowsOf(body).length <= 1) {
            // 最後の 1 行は消しません。空の台本は保存できず、行が 0 だと
            // 写して増やす元も無くなります。
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

    // --- 読みの確認 ---------------------------------------------------------

    // 読みは自明ではありません（「水面」は ミナモ ではなく スイメン、算用数字も文脈で
    // 変わります）。台本ぶんの合成時間を使ってから気付くと、その時間がそのまま無駄に
    // なります。返すのはサーバーで、合成の直前と同じ変換を通すため、ここに出るものが
    // 実際に読まれるものです。
    //
    // 表の中身をそのまま送ります。保存済みの台本ではありません。直した行の読みを
    // 確かめたいのに、保存しないと確かめられないのでは、直す前に戻ってしまいます。

    // show は、返ってきた読みを各行の下へ差し込みます。
    const show = (fields, lines) => {
        fields.forEach((field, index) => {
            const target = field.parentNode.querySelector('.js-reading');
            const reading = lines[index]?.reading;
            if (target && reading !== undefined) {
                target.textContent = reading;
            }
        });
    };

    // clearReadings は差し込んだ読みを消します。送り直すたびに前回の結果が残らないように。
    const clearReadings = () => {
        for (const node of document.querySelectorAll('.js-reading')) {
            node.textContent = '';
        }
    };

    // failureMessage は、応答の {"error": "..."} を取り出します。読めない応答でも
    // 画面には何か出します（黙って何も起きないのがいちばん困ります）。
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
        // 超えた分を送ってから 400 で返されるより、押した時点で理由を出すほうが分かります。
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
                // 空の行も落としません。応答は要求と同じ並びで返るので、番号がずれます。
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

    // --- 組み立て -----------------------------------------------------------

    document.addEventListener('DOMContentLoaded', () => {
        const body = document.querySelector('#script-form tbody');
        if (!body) {
            return;
        }

        // 行は増えるので、行ごとではなく表に 1 つだけ張ります。写した行にもそのまま
        // 効くため、複製のたびにイベントを張り直す必要がありません。
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

        // 初回。保存済みの値 1 つだけが入っている状態から、話者ぶんへ広げます。
        rowsOf(body).forEach(fillRow);
        refresh(body);

        const previewButton = document.querySelector('.js-preview-reading');
        const previewStatus = document.querySelector('.js-reading-status');
        if (previewButton && previewStatus) {
            previewButton.addEventListener('click', () => preview(previewButton, previewStatus));
        }
    });
})();
