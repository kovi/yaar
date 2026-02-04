import { API } from '../api/ApiClient.js';
import { Format } from '../api/Format.js';
import { openFileInfo } from './FileInfo.js';

const ICON_LOCATE = `
<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
    <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"></path>
    <circle cx="12" cy="13" r="3"></circle>
</svg>`;

export async function StreamManager(path) {
    const segments = path.split('/').filter(s => s && s !== '_');

    // segments[0] is 'streams'
    if (segments.length === 1) {
        return renderStreamList();
    } else {
        const streamName = segments[1];
        return renderStreamDetail(streamName);
    }
}

async function renderStreamList() {
    const streams = await API.getStreams();
    const view = document.createElement('div');

    view.innerHTML = `
        <nav class="af-breadcrumb">
            <div class="af-breadcrumb-item"><span class="af-breadcrumb-current">📡 Streams</span></div>
        </nav>
        <div class="af-table-wrapper">
            <table class="af-table af-table-clickable">
                <thead>
                    <tr>
                        <th>Stream Identifier</th>
                        <th style="text-align: right; color: var(--text-muted); font-weight: normal; font-size: 11px;">Click row to open</th>
                    </tr>
                </thead>
                <tbody id="stream-list-body"></tbody>
            </table>
        </div>
    `;

    const tbody = view.querySelector('#stream-list-body');

    if (streams.length == 0) {
        const row = document.createElement('tr');
        row.innerHTML = `<td colspan="2">No streams</td>`;
        tbody.appendChild(row)
    }
    streams.forEach(s => {
        const row = document.createElement('tr');
        row.classList.add("af-file-row");
        row.innerHTML = `
            <td><strong class="af-link-text"><span class="badge badge-origin badge-stream">${s}</span></strong></td>
            <td style="text-align: right; color: var(--border);">→</td>
        `;

        // Row-level navigation logic
        row.onclick = () => {
            window.history.pushState(null, '', `/_/streams/${s}`);
            // Trigger the SPA router
            window.dispatchEvent(new CustomEvent('artifactory:navigated'));
        };

        tbody.appendChild(row);
    });

    return view;
}


async function renderStreamDetail(name) {
    const groups = await API.getStreamGroups(name);
    const view = document.createElement('div');

    view.innerHTML = `
        <nav class="af-breadcrumb">
            <div class="af-breadcrumb-item"><a href="/_/streams" class="af-breadcrumb-link nav-link">📡 Streams</a></div>
            <div class="af-breadcrumb-item"><span class="af-breadcrumb-current">${name}</span></div>
        </nav>

        <div class="af-table-wrapper">
            <table class="af-table af-table-compact af-table-grouped">
                <thead>
                    <tr>
                        <th>Resource / Group</th>
                        <th style="width: 100px;">Size</th>
                        <th style="width: 150px;">Created</th>
                        <th style="width: 50px;"></th>
                    </tr>
                </thead>
                <tbody id="grouped-body"></tbody>
            </table>
        </div>
    `;

    const tbody = view.querySelector('#grouped-body');

    if (groups === null || groups.length === 0) {
        const groupRow = document.createElement('tr');
        groupRow.className = 'af-group-row';
        groupRow.innerHTML = `
            <td colspan="4" class="af-group-header-cell">
                No groups
            </td>
        `;
        tbody.appendChild(groupRow);
    } else {
        const fragment = document.createDocumentFragment();
        groups.forEach(group => {
            // 1. Group Header Row (Using the Badge Visual from FileBrowser)
            const groupRow = document.createElement('tr');
            groupRow.className = 'af-group-row';
            groupRow.innerHTML = `
            <td colspan="4" class="af-group-header-cell">
                <span class="badge-origin badge-group">${name}/${group.name}</span>
                <span class="af-group-badge">${group.files.length} items</span>
            </td>`;
            fragment.appendChild(groupRow);

            // 2. File Rows
            // Helper to get parent folder from a file path
            function getParentDir(path) {
                if (!path || path === '/') return '/';
                const parts = path.split('/').filter(p => p);
                if (parts.length <= 1) return '/';
                return '/' + parts.slice(0, -1).join('/');
            }

            // Inside the fileRow loop in renderStreamDetail:
            group.files.forEach(file => {
                const fileRow = document.createElement('tr');
                fileRow.className = 'af-file-row';

                const parentDir = getParentDir(file.name);
                const filename = file.name.substring(file.name.lastIndexOf("/") + 1)

                fileRow.innerHTML = `
                    <td class="af-file-indent">
                        <div style="display:flex; align-items:center;">
                            <span class="af-file-icon">${Format.getFileIcon(file)}</span>
                            <div class="af-file-details">
                                <div style="display:flex; align-items:center;">
                                    <a href="${file.name}" class="af-file-name">${file.name}</a>
                                    <a href="${parentDir}?highlight=${filename}" class="nav-link af-locate-btn" title="Open containing folder">
                                        ${ICON_LOCATE} Go to folder
                                    </a>
                                </div>
                            </div>
                        </div>
                    </td>
                    <td class="af-col-mono">${Format.formatBytes(file.size)}</td>
                    <td class="af-col-mono" style="font-size:11px">${Format.dateTime(file.modtime)}</td>
                    <td>
                        <div class="af-row-actions">
                            <button class="btn btn-ghost info-btn">ℹ️</button>
                        </div>
                    </td>
                `;

                fileRow.querySelector('.info-btn').onclick = () => openFileInfo(file);

                fragment.appendChild(fileRow);
            });
            tbody.appendChild(fragment);
        });
    }
    return view;
}