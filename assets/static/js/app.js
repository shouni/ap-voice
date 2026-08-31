// 全画面共通の振る舞いです。layout.html が defer で最初に読み込むため、
// ページ個別のスクリプトより先に走ります（defer は記述順を保ちます）。
//
// ここに置くのは、どの画面にもありうる操作だけです。画面が 1 つしか使わない
// ものはページ個別のスクリプトへ置いてください（対応は handlers の pageScripts）。
(function () {
    'use strict';

    // 送信前の確認は data-confirm で宣言します。onsubmit 属性のままだと CSP の
    // script-src に 'unsafe-inline' が必要になり、インラインスクリプト禁止が無意味になります。
    //
    // 画面ごとに書かないのは、取り返しの付かない操作が増えるたびに同じ 5 行を
    // 写すことになるからです。宣言はテンプレート側の属性 1 つで済みます。
    document.addEventListener('submit', function (event) {
        var form = event.target.closest('form[data-confirm]');
        if (form && !window.confirm(form.dataset.confirm)) {
            event.preventDefault();
        }
    });
})();
