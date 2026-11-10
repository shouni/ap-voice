// Utilities and behaviour shared by every page. layout.html loads this first with defer
// (defer keeps document order), so what is defined here can be used synchronously by the page scripts.
//
// Only what any page might have belongs here. Something only one page uses goes in
// that page's script (the mapping is pageScripts in handlers).
window.App = window.App || {};

(() => {
    'use strict';

    const App = window.App;

    // csrfToken returns the token embedded in the page.
    //
    // A page has several forms, but the value comes from the session and is the same, so
    // the first one found is enough. The JS need not know the forms' ids.
    App.csrfToken = () => document.querySelector('input[name="csrf_token"]')?.value || '';

    // Confirmation before submit is declared with data-confirm. An onsubmit attribute would need
    // 'unsafe-inline' in the CSP's script-src, making the ban on inline scripts meaningless.
    //
    // It is not written per page because every new irreversible operation would copy the same few
    // lines. The declaration is a single attribute in the template.
    document.addEventListener('submit', (event) => {
        const form = event.target.closest('form[data-confirm]');
        if (form && !window.confirm(form.dataset.confirm)) {
            event.preventDefault();
        }
    });

    // deleteResource asks for confirmation, sends DELETE, and navigates on success.
    //
    // HTML forms cannot send DELETE, so deletion alone uses fetch. Having a separate
    // POST …/delete for the page would make two of the same deletion, splitting the entry points for authorization and logging.
    App.deleteResource = async ({url, confirmMessage, redirect, failureMessage = '削除に失敗しました。'}) => {
        if (!window.confirm(confirmMessage)) {
            return false;
        }
        try {
            const response = await fetch(url, {
                method: 'DELETE',
                headers: {'Accept': 'application/json', 'X-CSRF-Token': App.csrfToken()}
            });
            if (!response.ok) {
                const text = await response.text();
                window.alert(text ? `${failureMessage}: ${text}` : failureMessage);
                return false;
            }
            if (redirect) {
                window.location.href = redirect;
            }
            return true;
        } catch (error) {
            console.error('Delete Error:', error);
            window.alert('通信エラーが発生しました。');
            return false;
        }
    };

    // A delete button with data-url works by declaration alone (three attributes in the template).
    document.addEventListener('click', (event) => {
        const button = event.target.closest('button[data-url][data-confirm]');
        if (!button) return;
        event.preventDefault();
        App.deleteResource({
            url: button.dataset.url,
            confirmMessage: button.dataset.confirm,
            redirect: button.dataset.redirect
        });
    });
})();
