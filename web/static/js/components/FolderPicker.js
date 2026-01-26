import { API } from '../api/ApiClient.js';

const template = document.createElement('template');
template.innerHTML = `
    <dialog class="af-modal" id="folder-picker-dialog" style="max-width: 500px;">
        <div class="af-modal-header">
            <h3>Select Destination</h3>
            <button type="button" class="btn btn-ghost modal-close">×</button>
        </div>
        <div class="af-modal-body" style="padding: 0;">
            <nav class="af-breadcrumb" id="picker-breadcrumb" style="margin: 15px; border: none;"></nav>
            <div class="af-table-wrapper" style="border:none; border-radius: 0; border-top: 1px solid var(--border);">
                <table class="af-table af-table-compact af-table-clickable">
                    <tbody id="folder-list"></tbody>
                </table>
            </div>
        </div>
        <div class="af-modal-footer">
            <button type="button" class="btn btn-ghost modal-close">Cancel</button>
            <button type="button" class="btn btn-primary" id="confirm-folder-btn">Select Current Folder</button>
        </div>
    </dialog>
`;

export async function pickFolder(startPath = '/') {
    return new Promise((resolve) => {
        if (!document.getElementById('folder-picker-dialog')) {
            document.body.appendChild(template.content.cloneNode(true));
        }

        const dialog = document.getElementById('folder-picker-dialog');
        const list = dialog.querySelector('#folder-list');
        const bc = dialog.querySelector('#picker-breadcrumb');
        const confirmBtn = dialog.querySelector('#confirm-folder-btn');
        let currentPath = startPath;

        const render = async (path) => {
            currentPath = path;
            const data = await API.listFiles(path);
            const folders = Object.values(data || []).filter(f => f.isdir);

            // Render Breadcrumbs
            bc.innerHTML = renderBreadcrumbs(path);

            // Render Folder List
            list.innerHTML = '';

            // Add "Up" row if not at root
            if (path !== '/') {
                const parent = path.substring(0, path.lastIndexOf('/')) || '/';
                const upRow = document.createElement('tr');
                upRow.innerHTML = `<td colspan="2"><span style="margin-right:8px">⤴️</span> ..</td>`;
                upRow.onclick = () => render(parent);
                list.appendChild(upRow);
            }

            folders.forEach(f => {
                const row = document.createElement('tr');
                row.innerHTML = `
                    <td style="width: 30px; text-align:center;">📁</td>
                    <td>${f.name}</td>
                `;
                row.onclick = () => render((path + '/' + f.name).replace(/\/+/g, '/'));
                list.appendChild(row);
            });

            if (folders.length === 0 && path === '/') {
                list.innerHTML = `<tr><td colspan="2" class="af-text-muted" style="text-align:center; padding:20px;">No folders found</td></tr>`;
            }
        };

        // UI Event Listeners
        dialog.querySelectorAll('.modal-close').forEach(btn => {
            btn.onclick = () => { dialog.close(); resolve(null); };
        });

        confirmBtn.onclick = () => {
            dialog.close();
            resolve(currentPath);
        };

        // Use global nav-link interceptor logic for breadcrumbs inside the picker
        bc.onclick = (e) => {
            const link = e.target.closest('.nav-link');
            if (link) {
                e.preventDefault();
                e.stopPropagation();
                render(link.getAttribute('href'));
            }
        };

        render(startPath);
        dialog.showModal();
    });
}

// Minimal internal breadcrumb helper
function renderBreadcrumbs(path) {
    const parts = path.split('/').filter(p => p);
    let current = '';
    let html = `<div class="af-breadcrumb-item"><a href="/" class="af-breadcrumb-link nav-link">🏠 Root</a></div>`;
    parts.forEach(p => {
        current += '/' + p;
        html += `<div class="af-breadcrumb-item"><a href="${current}" class="af-breadcrumb-link nav-link">${p}</a></div>`;
    });
    return html;
}
