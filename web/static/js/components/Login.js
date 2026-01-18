import { API } from '../api/ApiClient.js';
import { Auth } from '../api/Auth.js';

const template = document.createElement('template');
template.innerHTML = `
    <dialog class="af-modal" id="login-dialog">
        <form class="af-form">
            <div class="af-modal-header">
                <h3>Sign In</h3>
            </div>
            <div class="af-modal-body">
                <div id="login-error" class="badge badge-danger hidden" style="margin-bottom:10px; width:100%"></div>
                <label>
                    <span>Username</span>
                    <input type="text" name="username" required autocomplete="username">
                </label>
                <label>
                    <span>Password</span>
                    <input type="password" name="password" required autocomplete="current-password">
                </label>
            </div>
            <div class="af-modal-footer">
                <button type="button" class="btn btn-ghost modal-close">Cancel</button>
                <button type="submit" class="btn btn-primary">Login</button>
            </div>
        </form>
    </dialog>
`;

export function openLogin() {
    if (!document.getElementById('login-dialog')) {
        document.body.appendChild(template.content.cloneNode(true));
        const dialog = document.getElementById('login-dialog');
        const form = dialog.querySelector('form');

        form.onsubmit = async (e) => {
            e.preventDefault();
            const fd = new FormData(form);
            try {
                const data = await API.login(Object.fromEntries(fd));
                Auth.saveSession(data);
                dialog.close();
                window.location.reload();
            } catch (err) {
                const errBox = dialog.querySelector('#login-error');
                errBox.textContent = err.message;
                errBox.classList.remove('hidden');
            }
        };

        dialog.querySelector('.modal-close').onclick = () => dialog.close();
    }
    document.getElementById('login-dialog').showModal();
}