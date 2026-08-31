// 全画面共通のユーティリティと振る舞いです。layout.html が defer で最初に読み込むため
// （defer は記述順を保つ）、ここで定義したものはページ個別のスクリプトから同期的に使えます。
//
// ここに置くのは、どの画面にもありうるものだけです。画面が 1 つしか使わないものは
// ページ個別のスクリプトへ置いてください（対応は handlers の pageScripts）。
window.App = window.App || {};

(() => {
    'use strict';

    const App = window.App;

    // csrfToken は、画面に埋まっているトークンを返します。
    //
    // 1 つの画面に複数のフォームがありますが、値はセッションから来る同じものなので
    // 最初に見つかったもので足ります。フォームの id を JS 側が知る必要はありません。
    App.csrfToken = () => document.querySelector('input[name="csrf_token"]')?.value || '';

    // 送信前の確認は data-confirm で宣言します。onsubmit 属性のままだと CSP の
    // script-src に 'unsafe-inline' が必要になり、インラインスクリプト禁止が無意味になります。
    //
    // 画面ごとに書かないのは、取り返しの付かない操作が増えるたびに同じ数行を写すことに
    // なるからです。宣言はテンプレート側の属性 1 つで済みます。
    document.addEventListener('submit', (event) => {
        const form = event.target.closest('form[data-confirm]');
        if (form && !window.confirm(form.dataset.confirm)) {
            event.preventDefault();
        }
    });
})();
