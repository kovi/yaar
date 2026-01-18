import { API } from '../api/ApiClient.js';
import { Auth } from '../api/Auth.js';

export async function UserManagement() {
    const container = document.createElement('div');
    const users = await API.getUsers();
    const currentUser = Auth.getUser();

    container.innerHTML = `
        <div class="af-actions" style="margin-bottom: 15px;">
            <button class="btn btn-primary btn-sm" id="add-user-btn">+ Add User</button>
        </div>
        <div class="af-table-wrapper">
            <table class="af-table af-table-compact">
                <thead>
                    <tr>
                        <th>Username</th>
                        <th>Role</th>
                        <th style="text-align:right">Actions</th>
                    </tr>
                </thead>
                <tbody id="user-table-body">
                    ${users.map(u => `
                        <tr>
                            <td><strong>${u.username}</strong></td>
                            <td>${u.is_admin ? '<span class="badge badge-info">Admin</span>' : '<span class="badge badge-outline">User</span>'}</td>
                            <td style="text-align:right">
                                <button class="btn btn-ghost edit-user" data-id="${u.id}">Edit</button>
                                <button class="btn btn-ghost btn-danger delete-user" data-id="${u.id}" 
                                    ${u.username === currentUser.username ? 'disabled' : ''}>Delete</button>
                            </td>
                        </tr>
                    `).join('')}
                </tbody>
            </table>
        </div>
    `;

    // Listeners for Edit/Delete/Add...
    container.querySelector('#add-user-btn').onclick = () => openUserForm();
    container.querySelectorAll('.edit-user').forEach((btn, i) => {
        btn.onclick = () => openUserForm(users[i]);
    });

    container.querySelectorAll('.delete-user').forEach((btn, i) => {
        btn.onclick = async () => {
            if (confirm(`Delete user ${users[i].username}?`)) {
                await API.deleteUser(users[i].id);
                window.dispatchEvent(new CustomEvent('af:settings-refresh'));
            }
        };
    });

    return container;
}

function openUserForm(user = null) {
    const isEdit = !!user;
    const dialog = document.createElement('dialog');
    dialog.className = 'af-modal';
    dialog.style.zIndex = '1100'; // Above the settings modal

    dialog.innerHTML = `
        <form method="dialog" class="af-form">
            <div class="af-modal-header">
                <h3>${isEdit ? 'Edit User' : 'Create User'}</h3>
            </div>
            <div class="af-modal-body">
                <label>
                    <span>Username</span>
                    <input type="text" name="username" value="${user?.username || ''}" ${isEdit ? 'readonly' : 'required'}>
                </label>
                <label>
                    <span>${isEdit ? 'New Password (leave blank to keep)' : 'Password'}</span>
                    <input type="password" name="password" ${isEdit ? '' : 'required'}>
                </label>
                <label class="af-check-group">
                    <input type="checkbox" name="is_admin" ${user?.is_admin ? 'checked' : ''}>
                    <span>Administrator Privileges</span>
                </label>
                
                <div style="margin-top: 15px; padding-top: 15px; border-top: 1px dashed var(--border)">
                    <label>
                        <span>Allowed Paths (Experimental)</span>
                        <input type="text" name="allowed_paths" placeholder="/projects/A, /public" disabled>
                        <small class="af-input-help">Restrict user to specific directories.</small>
                    </label>
                </div>
            </div>
            <div class="af-modal-footer">
                <button type="button" class="btn btn-ghost" id="form-cancel">Cancel</button>
                <button type="submit" class="btn btn-primary">Save User</button>
            </div>
        </form>
    `;

    document.body.appendChild(dialog);
    dialog.showModal();

    dialog.querySelector('#form-cancel').onclick = () => { dialog.close(); dialog.remove(); };
    dialog.querySelector('form').onsubmit = async (e) => {
        const fd = new FormData(e.target);
        const data = {
            password: fd.get('password') || undefined,
            is_admin: fd.get('is_admin') === 'on'
        };
        if (!isEdit) data.username = fd.get('username');

        try {
            if (isEdit) await API.updateUser(user.id, data);
            else await API.createUser(data);
            dialog.close();
            dialog.remove();
            window.dispatchEvent(new CustomEvent('af:settings-refresh'));
        } catch (err) { alert(err.message); }
    };
}
