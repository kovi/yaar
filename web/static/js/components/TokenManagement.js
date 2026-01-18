import { API } from '../api/ApiClient.js';
import { Format } from '../api/Format.js';
import * as ExpirationLabel from './ExpirationLabel.js'

export async function TokenManagement() {
    const container = document.createElement('div');
    const [tokens, users] = await Promise.all([API.getTokens(), API.getUsers()]);

    container.innerHTML = `
        <div class="af-actions" style="margin-bottom: 15px;">
            <button class="btn btn-primary btn-sm" id="gen-token-btn">+ Generate New Token</button>
        </div>
        <div class="af-table-wrapper">
            <table class="af-table af-table-compact">
                <thead>
                    <tr>
                        <th>Token Name</th>
                        <th>User</th>
                        <th>Scope</th>
                        <th>Expires</th>
                        <th>Last Used</th>
                        <th style="text-align:right">Actions</th>
                    </tr>
                </thead>
                <tbody id="token-table-body">
                    ${tokens.map(t => `
                        <tr>
                            <td><strong>${t.Name}</strong></td>
                            <td><span class="badge badge-outline">${t.User?.username || 'System'}</span></td>
                            <td><code class="af-col-mono" style="font-size: 11px;">${t.PathScope}</code></td>
                            <td class="af-col-mono" style="font-size: 11px;">
                                ${t.expires_at ? `
                                    <span class="${Format.isExpired(t.expires_at) ? 'expiry-critical' : ''}">
                                        ${Format.dateTime(t.expires_at)}
                                    </span>
                                ` : '<span class="af-text-muted">Never</span>'}
                            </td>
                            <td class="af-col-mono" style="font-size: 11px;">${Format.dateTime(t.last_used_at)}</td>
                            <td style="text-align:right">
                                <button class="btn btn-ghost btn-danger revoke-token" data-id="${t.ID}">Revoke</button>
                            </td>
                        </tr>
                    `).join('')}
                </tbody>
            </table>
        </div>
    `;

    container.querySelector('#gen-token-btn').onclick = () => openTokenForm(users);

    container.querySelectorAll('.revoke-token').forEach((btn, i) => {
        btn.onclick = async () => {
            if (confirm(`Revoke token "${tokens[i].Name}"?`)) {
                await API.deleteToken(tokens[i].ID);
                window.dispatchEvent(new CustomEvent('af:settings-refresh'));
            }
        };
    });

    return container;
}

function openTokenForm(users) {
    const dialog = document.createElement('dialog');
    dialog.className = 'af-modal';
    dialog.style.zIndex = '1100';

    dialog.innerHTML = `
        <form method="dialog" class="af-form">
            <div class="af-modal-header"><h3>Generate Automation Token</h3></div>
            <div class="af-modal-body">
                <label>
                    <span>Description / Name</span>
                    <input type="text" name="name" placeholder="e.g. Jenkins-Prod-Deploy" required>
                </label>
                <label>
                    <span>Assign to User</span>
                    <select name="user_id" class="af-select" required>
                        ${users.map(u => `<option value="${u.id}">${u.username}</option>`).join('')}
                    </select>
                </label>
                <label>
                    <span>Path Scope (Restriction)</span>
                    <input type="text" name="path_scope" value="/" required>
                    <small class="af-input-help">The token will only be allowed to write under this path.</small>
                </label>
                ${ExpirationLabel.LABEL_TEMPLATE}
            </div>
            <div class="af-modal-footer">
                <button type="button" class="btn btn-ghost" id="token-cancel">Cancel</button>
                <button type="submit" class="btn btn-primary">Generate Token</button>
            </div>
        </form>
    `;
    ExpirationLabel.setupExpiryPicker(dialog);

    document.body.appendChild(dialog);
    dialog.showModal();

    dialog.querySelector('#token-cancel').onclick = () => { dialog.close(); dialog.remove(); };
    dialog.querySelector('form').onsubmit = async (e) => {
        const fd = new FormData(e.target);
        try {
            const result = await API.createToken({
                name: fd.get('name'),
                user_id: parseInt(fd.get('user_id')),
                path_scope: fd.get('path_scope'),
                expires: Format.durationToBackendFormat(fd.get('expires')),
            });
            dialog.close();
            dialog.remove();
            showSecretToken(result.plain_token); // Show the one-time secret
            window.dispatchEvent(new CustomEvent('af:settings-refresh'));
        } catch (err) { alert(err.message); }
    };
}

function showSecretToken(plainToken) {
    const dialog = document.createElement('dialog');
    dialog.className = 'af-modal';
    dialog.style.zIndex = '1200';

    dialog.innerHTML = `
        <div class="af-modal-header"><h3>Token Generated Successfully</h3></div>
        <div class="af-modal-body">
            <p style="font-size: 13px; margin-bottom: 15px;">Copy this secret now. It will <strong>never</strong> be shown again.</p>
            <div class="af-hash-box" style="padding: 15px; background: #fffbe6; border: 1px solid #ffe58f;">
                <code class="af-col-mono" id="plain-token-val" style="font-size: 16px; color: #856404;">${plainToken}</code>
            </div>
            <div style="margin-top: 20px;">
                <button class="btn btn-primary" id="copy-token-btn" style="width: 100%">Copy to Clipboard</button>
            </div>
        </div>
        <div class="af-modal-footer">
            <button type="button" class="btn btn-ghost" id="secret-close">I have saved the token</button>
        </div>
    `;

    document.body.appendChild(dialog);
    dialog.showModal();

    const closeBtn = dialog.querySelector('#secret-close');
    const copyBtn = dialog.querySelector('#copy-token-btn');

    copyBtn.onclick = async () => {
        await navigator.clipboard.writeText(plainToken);
        copyBtn.textContent = '✅ Copied!';
        copyBtn.classList.replace('btn-primary', 'btn-success');
    };

    closeBtn.onclick = () => { dialog.close(); dialog.remove(); };
}