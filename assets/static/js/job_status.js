// 実行中のジョブを見張り、状態が変わったら画面を読み直します。
//
// 投入したあと、この画面には「実行中です」と出るだけで、完了したかどうかは
// 手で再読み込みするまで分かりませんでした。合成は分単位かかるので、
// 押した人は待つか、あきらめて Slack の通知を待つことになります。
//
// 状態は既にサーバーが返しています（GET /jobs/{id} の JSON）。ここでやるのは
// それを定期的に読み、記録が動いたら画面ごと読み直すことだけです。台本や音声の
// 有無まで画面側で組み立てると、サーバーの描画と二重になります。
(() => {
    'use strict';

    // 合成は分単位かかるので、秒単位で叩く意味がありません。ジョブ 1 件の上限
    // （PIPELINE_TIMEOUT の既定 25 分）に対して十分細かく、かつ静かな間隔にします。
    const INTERVAL_MS = 15000;

    // 見張りをやめるまでの上限です。開きっぱなしのタブが延々と叩き続けるのを
    // 防ぎます。ジョブの上限（25 分）を少し超えたところで止めます。
    const MAX_POLLS = 120;

    // 終わった状態です。ここに落ちたら、この画面に出すものは確定しています。
    const TERMINAL = ['succeeded', 'failed'];

    let polls = 0;

    const check = async (holder, timer) => {
        polls += 1;
        if (polls > MAX_POLLS) {
            window.clearInterval(timer);
            return;
        }

        try {
            const response = await fetch(holder.dataset.endpoint, {
                headers: {'Accept': 'application/json'},
                // 履歴の状態はブラウザに溜めさせません。溜まると、変わったことに
                // 気付くために見張っているのに古い応答を読み続けます。
                cache: 'no-store'
            });
            if (!response.ok) {
                // 記録が無い（404）ジョブは、待っても状態が生えません。
                // 読めないだけの場合も、画面から直せることはありません。
                window.clearInterval(timer);
                return;
            }

            const status = await response.json();
            if (status?.state && status.state !== holder.dataset.state) {
                // 変わったのはここまでで分かります。何がどう変わったかは
                // サーバーが描き直したものを見ます。
                window.clearInterval(timer);
                window.location.reload();
            }
        } catch {
            // 通信できないだけなら、次の周期で拾い直せます。連続で失敗しても
            // MAX_POLLS で止まります。
        }
    };

    document.addEventListener('DOMContentLoaded', () => {
        const holder = document.getElementById('job-state');
        if (!holder?.dataset.endpoint) {
            return;
        }
        // 終わっているジョブは見張りません。開くたびに 1 往復増えるだけです。
        if (!holder.dataset.state || TERMINAL.includes(holder.dataset.state)) {
            return;
        }

        const timer = window.setInterval(() => check(holder, timer), INTERVAL_MS);
    });
})();
