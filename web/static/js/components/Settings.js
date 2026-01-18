import { SystemInfo } from './SystemInfo.js';
import { UserManagement } from './UserManagement.js';
import { TokenManagement } from './TokenManagement.js';

const template = document.createElement('template');
template.innerHTML = `
    <dialog class="af-modal" id="settings-dialog" style="max-width: 600px;">
        <div class="af-modal-header">
            <h3>Settings & Administration</h3>
            <button type="button" class="btn btn-ghost modal-close">×</button>
        </div>
        
        <nav class="af-tabs">
            <button class="af-tab-btn active" data-tab="system">System</button>
            <button class="af-tab-btn af-admin-only" data-tab="users">Users</button>
            <button class="af-tab-btn af-admin-only" data-tab="tokens">Tokens</button>
        </nav>

        <div class="af-modal-body">
            <!-- System Tab -->
            <div class="af-tab-content active" id="tab-system"></div>

            <!-- Users Tab -->
            <div class="af-tab-content" id="tab-users"></div>

            <!-- Tokens Tab -->
            <div class="af-tab-content" id="tab-tokens"></div>
        </div>

        <div class="af-modal-footer">
            <button type="button" class="btn btn-primary modal-close">Close</button>
        </div>
    </dialog>
`;

export async function openSettings() {
    if (!document.getElementById('settings-dialog')) {
        document.body.appendChild(template.content.cloneNode(true));

        const dialog = document.getElementById('settings-dialog');
        dialog.querySelectorAll('.modal-close').forEach(btn => {
            btn.onclick = () => dialog.close();
        });
    }

    const dialog = document.getElementById('settings-dialog');
    const user = JSON.parse(localStorage.getItem('af_user'));

    // 1. Hide Admin Tabs if user is not admin
    dialog.querySelectorAll('.af-admin-only').forEach(el => {
        el.style.display = user.isAdmin ? 'block' : 'none';
    });

    // 2. Tab Switching Logic
    dialog.querySelectorAll('.af-tab-btn').forEach(btn => {
        btn.onclick = async () => {
            dialog.querySelectorAll('.af-tab-btn, .af-tab-content').forEach(el => el.classList.remove('active'));
            btn.classList.add('active');
            const target = dialog.querySelector(`#tab-${btn.dataset.tab}`);
            target.classList.add('active');

            // Lazy load user management
            if (btn.dataset.tab === 'users') {
                target.replaceChildren(await UserManagement());
            } else if (btn.dataset.tab === 'system') {
                target.replaceChildren(await SystemInfo());
            }
            else if (btn.dataset.tab === 'tokens') {
                target.replaceChildren(await TokenManagement());
            }
        };
    });

    // Initial activation of first tab
    dialog.querySelector('.af-tab-btn').click();
    dialog.showModal();
}

// Helper to allow child components to refresh the view
window.addEventListener('af:settings-refresh', async () => {
    const activeTab = document.querySelector('.af-tab-btn.active');
    if (!activeTab) { return; }

    const target = document.querySelector(`#tab-${activeTab.dataset.tab}`);
    if (activeTab.dataset.tab === 'users') {
        target.replaceChildren(await UserManagement());
    } else if (activeTab.dataset.tab === 'tokens') {
        target.replaceChildren(await TokenManagement());
    }
});