
import { API } from '../api/ApiClient.js';
import { Format } from '../api/Format.js';
import { openFileInfo } from './FileInfo.js';
import { openUploadDialog } from './UploadDialog.js';
import * as ExpirationLabel from './ExpirationLabel.js'

const ICON_PARENT = `
<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
    <line x1="12" y1="19" x2="12" y2="5"></line>
    <polyline points="5 12 12 5 19 12"></polyline>
</svg>`;

/** 
 * Define the template for the sub page
 * This is parsed only ONCE when the module is loaded.
 */
const template = document.createElement('template');
template.innerHTML = `
    <nav class="af-breadcrumb" id="breadcrumb"></nav>

    <div class="af-actions">
        <button id="new-directory-btn" class="btn btn-ghost af-requires-auth">New directory</button>
        <button id="upload-btn" class="btn btn-primary af-requires-auth">Upload</button>
    </div>

    <div class="af-table-wrapper">
        <table class="af-table af-table-compact">
            <thead>
                <tr>
                    <th class="sortable" data-sort="name">Name <span class="indicator"></span></th>
                    <th class="sortable" data-sort="size" style="width: 100px;">Size <span class="indicator"></span></th>
                    <th class="sortable" data-sort="modtime" style="width: 180px;">Modified <span class="indicator"></span></th>
                    <th style="width: 80px;"></th>
                </tr>
            </thead>
            <tbody id="file-list"></tbody>
        </table>
    </div>
`;

/**
 * Define a template for the individual ROWS.
 */
const rowTemplate = document.createElement('template');
rowTemplate.innerHTML = `
    <tr class="af-file-row">
        <td>
            <div class="af-file-wrapper">
                <a class="nav-link af-file-link">
                    <span class="af-file-icon"></span>
                    <span class="af-file-text"></span>
                </a>
            </div>
            <div class="af-attr-row">
                <span class="origin-info"></span>
                <div class="tags-container" style="display: contents;"></div>
                <span class="expiry-info"></span>
            </div>
        </td>
        <td class="af-col-mono size-cell"></td>
        <td class="af-col-mono time-cell"></td>
        <td>
            <div class="af-row-actions">
                <button class="btn btn-ghost info-btn" title="View Details">ℹ️</button>
                <button class="btn btn-ghost edit-btn af-requires-auth" title="Edit">📝</button>
                <button class="btn btn-ghost btn-danger del-btn af-requires-auth" title="Delete">🗑️</button>
            </div>
        </td>
    </tr>
`;

const editModalTemplate = document.createElement('template');
editModalTemplate.innerHTML = `
    <dialog class="af-modal">
        <form method="dialog" class="af-form">
            <div class="af-modal-header">
                <h3>Edit File Properties</h3>
                <button type="button" class="btn btn-ghost modal-close">×</button>
            </div>
            <div class="af-modal-body">
                <label>
                    <span>Name</span>
                    <input type="text" name="new_name" id="edit-name-input">
                </label>
                <label>
                    <span>Tags (comma separated)</span>
                    <input type="text" name="tags" placeholder="env=prod, arch=x64">
                </label>
                ${ExpirationLabel.LABEL_TEMPLATE}
                <label>
                    <span>Stream</span>
                    <input type="text" name="stream">
                </label>
                <label class="af-check-group">
                    <input type="checkbox" name="keep_latest"> 
                    <span>Keep Latest</span>
                </label>
                <label class="af-check-group">
                    <input type="checkbox" name="immutable"> 
                    <span>Immutable</span>
                </label>
            </div>
            <div class="af-modal-footer">
                <button type="button" class="btn btn-ghost modal-close">Cancel</button>
                <button type="submit" class="btn btn-primary" id="save-btn">Save Changes</button>
            </div>
        </form>
    </dialog>
`;

let originalMeta;
function openEditModal(file, path) {
    const dialog = document.getElementById('edit-meta-dialog');
    const form = dialog.querySelector('form');

    // Store the "Original" state for comparison later
    // We normalize the values (e.g., dates) to match how they appear in form inputs
    originalMeta = {
        new_name: file.name,
        tags: file.tags ? file.tags.map(e => e.value ? `${e.key}=${e.value}` : e.key).join(", ") : "",
        immutable: file.policy.is_immutable || false,
        expires: file.expires_at || '',
        stream: file.stream && file.group ? `${file.stream}/${file.group}` : '',
        keep_latest: file.keep_latest,
    };

    form.dataset.path = path + "/" + file.name;

    // Populate the UI

    const new_name = form.querySelector('[name="new_name"]')
    new_name.value = originalMeta.new_name;
    let reasons = [];
    if (file.policy.is_immutable) reasons.push("Locked (Immutable)");
    if (file.policy.is_protected) reasons.push("Protected Path");
    if (!file.policy.is_allowed) reasons.push("Outside your scope");
    new_name.disabled = reasons.length > 0
    if (new_name.disabled)
        new_name.title = reasons[0]

    form.querySelector('[name="tags"]').value = originalMeta.tags;
    form.querySelector('[name="expires"]').value = Format.toHTMLInput(originalMeta.expires);
    form.querySelector('[name="immutable"]').checked = originalMeta.immutable;
    form.querySelector('[name="stream"]').value = originalMeta.stream;
    form.querySelector('[name="keep_latest"]').checked = originalMeta.keep_latest;
    ExpirationLabel.setupExpiryPicker(dialog);
    ExpirationLabel.setValue(dialog, originalMeta.expires);
    dialog.showModal();
}

async function onEditSubmit(e) {
    const dialog = document.getElementById('edit-meta-dialog');
    const form = dialog.querySelector('form');
    const fd = new FormData(form);
    let currentPath = form.dataset.path;
    const parentPath = currentPath.substring(0, currentPath.lastIndexOf('/'));

    const newName = fd.get('new_name');
    const hasRenamed = newName && newName !== originalMeta.new_name;
    try {
        if (hasRenamed) {
            await API.renameResource(currentPath, newName);
            // Update the path for the next step
            currentPath = (parentPath + '/' + newName).replace(/\/+/g, '/');
        }
    } catch (err) {
        alert(`Failed to save: ${err.message}`);
    }
    const payload = {};

    const currentTags = fd.get('tags');
    if (currentTags !== originalMeta.tags) payload.tags = currentTags;

    const currentImmutable = fd.get('immutable') === 'on';
    if (currentImmutable !== originalMeta.immutable) payload.immutable = currentImmutable;

    const currentKeepLatest = fd.get('keep_latest') === 'on';
    if (currentKeepLatest !== originalMeta.KeepLatest) payload.keep_latest = currentKeepLatest;

    const currentExpires = fd.get('expires')?.trim();
    if (currentExpires !== originalMeta.expires) {
        payload.expires_at = Format.durationToBackendFormat(currentExpires);
    }

    const currentStream = fd.get('stream');
    if (currentStream !== originalMeta.stream) {
        payload.stream = currentStream;
    }

    if (Object.keys(payload).length !== 0) {
        try {
            await API.patchEntry(currentPath, payload);
        } catch (err) {
            alert("Failed to patch file entry: " + err.message);
        }
    }

    dialog.close();
    form.reset();
    window.dispatchEvent(new CustomEvent('artifactory:refresh', { detail: { path: window.location.pathname } }));
}

function getDialogNode(id, template) {
    let dialog = document.getElementById(id);
    if (!dialog) {
        const node = template.content.cloneNode(true);
        node.children[0].id = id;
        document.body.appendChild(node);
        dialog = document.getElementById(id);
    }

    // need to reinitialize hooks for when view is recreated
    dialog.querySelectorAll('.modal-close').forEach(btn => {
        btn.onclick = () => dialog.close();
    });

    return dialog;
}

function updateSearchParams(col, nextOrder) {
    const newUrl = new URL(window.location.href);
    sessionStorage.setItem('af_pref_sort', col);
    sessionStorage.setItem('af_pref_order', nextOrder);

    const p = newUrl.searchParams;
    let changes = false;
    if (col == "name" && nextOrder == "asc") {
        // remove the default
        changes = p.has('sort') || p.has('order');
        p.delete('sort');
        p.delete('order');
    } else {
        changes = !p.has('sort', col) || !p.has('order', nextOrder);
        p.set('sort', col);
        p.set('order', nextOrder);
    }

    // Use replaceState so this sync doesn't create a "ghost" back-step
    if (!changes) {
        return;
    }
    window.history.replaceState({}, '', newUrl);
}

export async function FileBrowser(path) {
    const handleDelete = async (file) => {
        const fullPath = `${path}/${file.name}`.replace(/\/+/g, '/');

        if (confirm(`Are you sure you want to delete ${file.name}?`)) {

            if (file.isdir) {
                if (!confirm('You are about to delete a directory. All content will be removed recursively.\n\nContinue?')) {
                    return;
                }
            }

            try {
                await API.deleteFile(fullPath);

                // Success: Dispatch event to refresh the UI
                window.dispatchEvent(new CustomEvent('artifactory:refresh', {
                    detail: { path }
                }));
            } catch (err) {
                alert(`Delete failed: ${err.message}`);
            }
        }
    };

    const urlParams = new URLSearchParams(window.location.search);
    const highlightName = urlParams.get('highlight');

    // Sort state
    // Priority: URL (highest) > SessionStorage (tab-local) > Defaults
    let sortBy = urlParams.get('sort') || sessionStorage.getItem('af_pref_sort') || 'name';
    let sortOrder = urlParams.get('order') || sessionStorage.getItem('af_pref_order') || 'asc';
    updateSearchParams(sortBy, sortOrder);

    const files = await API.listFiles(path);

    if (!files || files.length === undefined) {
        const container = document.createElement('div');
        container.innerHTML = `
            <div class="af-card">
                Not a directory
            </div>
        `
        return container;
    }

    // Sorting Logic
    files.sort((a, b) => {
        // Rule 1: Directories always first
        if (a.isdir !== b.isdir) return a.isdir ? -1 : 1;

        // Rule 2: Sort by selected column
        let valA = a[sortBy];
        let valB = b[sortBy];

        // Handle types (strings vs numbers vs dates)
        if (sortBy === 'modtime') {
            valA = new Date(valA);
            valB = new Date(valB);
        } else if (sortBy === "name") {
            valA = valA.toLowerCase();
            valB = valB.toLowerCase();
        }

        if (valA < valB) return sortOrder === 'asc' ? -1 : 1;
        if (valA > valB) return sortOrder === 'asc' ? 1 : -1;
        return 0;
    });

    const content = template.content.cloneNode(true);

    const breadcrumbNav = content.getElementById('breadcrumb');
    breadcrumbNav.innerHTML = renderBreadcrumbs(path);

    // New directory 
    const newFolderBtn = content.getElementById('new-directory-btn');
    newFolderBtn.onclick = async () => {
        const folderName = prompt("Enter directory name:");
        if (!folderName) return;

        const fullPath = `${path}/${folderName}`.replace(/\/+/g, '/');
        try {
            await API.createDirectory(fullPath);
            window.dispatchEvent(new CustomEvent('artifactory:refresh', {
                detail: { path }
            }));
        } catch (err) {
            alert(err.message);
        }
    };

    // Edit dialog
    const editDialog = getDialogNode("edit-meta-dialog", editModalTemplate);
    editDialog.querySelector("form").onsubmit = onEditSubmit;


    // Upload functionality
    const uploadBtn = content.querySelector('#upload-btn');
    uploadBtn.onclick = () => openUploadDialog(path);

    // Update Header Indicators
    content.querySelectorAll('th.sortable').forEach(th => {
        const col = th.dataset.sort;
        if (col === sortBy) {
            th.querySelector('.indicator').textContent = sortOrder === 'asc' ? ' ↑' : ' ↓';
            th.classList.add('active-sort');
        }

        th.onclick = () => {
            const nextOrder = (col === sortBy && sortOrder === 'asc') ? 'desc' : 'asc';
            updateSearchParams(col, nextOrder);

            // Re-render the app
            window.dispatchEvent(new CustomEvent('artifactory:navigated'));
        };
    });

    // prepare file list
    const listBody = content.querySelector('#file-list');
    const fragment = document.createDocumentFragment();

    if (path != "/") {
        const row = rowTemplate.content.cloneNode(true);
        const tr = row.querySelector('.af-file-row');
        const link = row.querySelector('.af-file-link');
        const icon = row.querySelector('.af-file-icon');
        const text = row.querySelector('.af-file-text');

        tr.classList.add("af-back-row")
        icon.innerHTML = ICON_PARENT;
        const parent = path.substring(0, path.lastIndexOf("/", path.length - (path.endsWith('/') ? 2 : 1)) + 1)
        text.textContent = "..";
        link.href = parent;

        row.querySelector('.size-cell').textContent = '--';
        row.querySelector('.time-cell').textContent = "";
        row.querySelector(".af-row-actions").innerHTML = "";

        fragment.appendChild(row)
    }

    files.forEach(file => {
        const row = rowTemplate.content.cloneNode(true);

        const link = row.querySelector('.af-file-link');
        const icon = row.querySelector('.af-file-icon');
        const text = row.querySelector('.af-file-text');

        // Set Icon
        icon.textContent = Format.getFileIcon(file);

        // Set Name and Link
        text.textContent = file.name;
        link.href = (path + '/' + file.name).replace(/\/+/g, '/');
        if (!file.isdir) {
            link.target = "_new";
            link.classList.remove("nav-link")
        }

        row.querySelector('.size-cell').textContent = file.isdir ? '--' : Format.formatBytes(file.size);
        row.querySelector('.time-cell').textContent = Format.dateTime(file.modtime);

        // Origin Badges (Stream/Group)
        const originContainer = row.querySelector('.origin-info');
        if (file.stream) {
            originContainer.innerHTML += `<span class="badge-origin badge-stream">${file.stream}/${file.group}</span>`;
        }

        // Tags
        const tagsContainer = row.querySelector('.tags-container');
        if (file.tags && Array.isArray(file.tags)) {
            file.tags.forEach(tag => {
                const v = tag.value ? `=${tag.value}` : "";
                tagsContainer.innerHTML += `<span class="badge-tag">${tag.key}${v}</span>`;
            });
        }

        // Expiry
        if (file.expires_at && !file.expires_at.startsWith('0001')) {
            const expiryEl = row.querySelector('.expiry-info');
            expiryEl.textContent = `⏳ ${Format.timeRemaining(file.expires_at)}`;
            expiryEl.title = `Expires at ${Format.dateTime(file.expires_at)}`;

            if (Format.isExpired(file.expires_at)) {
                expiryEl.classList.add('expiry-critical');
            } else if (Format.isNearExpiry(file.expires_at, 24)) {
                expiryEl.classList.add('expiry-warning');
            }
        }

        if (highlightName && file.name === highlightName) {
            const tr = row.querySelector('.af-file-row');
            tr.classList.add('af-highlight-row');

            // Scroll into view after a tiny delay to ensure DOM rendering is complete
            setTimeout(() => {
                tr.scrollIntoView({ behavior: 'smooth', block: 'center' });
            }, 150);

            // Optional: Remove the parameter from the URL after a few seconds 
            // so refreshing doesn't keep highlighting forever
            setTimeout(() => {
                const cleanUrl = new URL(window.location.href);
                cleanUrl.searchParams.delete('highlight');
                window.history.replaceState(null, '', cleanUrl.pathname + cleanUrl.search);
            }, 3000);
        }

        // Automatically disable the Delete button in the row
        const delBtn = row.querySelector('.del-btn');
        delBtn.onclick = () => handleDelete(file);

        const policy = file.policy; // Passed from backend listing
        if (policy.is_immutable || policy.is_protected || !policy.is_allowed) {
            const dot = document.createElement('span');
            dot.className = 'af-policy-indicator';

            // Logic: Red if restricted, otherwise orange if protected, else gray
            if (!policy.is_allowed) dot.classList.add('restricted');
            else if (policy.is_protected || policy.is_immutable) dot.classList.add('protected');

            // The tooltip explains exactly why
            let reasons = [];
            if (policy.is_immutable) reasons.push("Locked (Immutable)");
            if (policy.is_protected) reasons.push("Protected Path");
            if (!policy.is_allowed) reasons.push("Outside your scope");

            dot.title = "Restrictions active: " + reasons.join(", ");
            text.appendChild(dot);

            if (delBtn) {
                delBtn.disabled = true;
                delBtn.title = "Delete disabled: " + reasons[0];
            }
        }

        row.querySelector('.info-btn').onclick = () => openFileInfo(file);
        row.querySelector('.edit-btn').onclick = () => openEditModal(file, path);

        fragment.appendChild(row);
    });

    listBody.appendChild(fragment);

    return content;
}

function renderBreadcrumbs(path) {
    const segments = path.split('/').filter(s => s.length > 0);
    let html = `
        <div class="af-breadcrumb-item">
            <a href="/" class="af-breadcrumb-link nav-link">🏠 Root</a>
        </div>
    `;

    let cumulativePath = '';
    segments.forEach((segment, index) => {
        cumulativePath += `/${segment}`;
        const isLast = index === segments.length - 1;
        if (isLast) {
            html += `<div class="af-breadcrumb-item"><span class="af-breadcrumb-current">${segment}</span></div>`;
        } else {
            html += `
                <div class="af-breadcrumb-item">
                    <a href="${cumulativePath}" class="af-breadcrumb-link nav-link">${segment}</a>
                </div>`;
        }
    });
    return html;
}

function calculateTimeRemaining(expiryStr) {
    const expiry = new Date(expiryStr);
    const now = new Date();
    const diff = expiry - now;
    if (diff < 0) return "Expired";

    const days = Math.floor(diff / (1000 * 60 * 60 * 24));
    if (days > 0) return `${days}d`;
    const hours = Math.floor(diff / (1000 * 60 * 60));
    return `${hours}h`;
}