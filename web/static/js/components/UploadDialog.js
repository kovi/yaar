import { TransferManager } from '../api/TransferManager.js';
import { Format } from '../api/Format.js';
import * as ExpirationLabel from './ExpirationLabel.js'

const template = document.createElement('template');
template.innerHTML = `
    <dialog class="af-modal" id="upload-dialog" style="max-width: 800px;">
        <form class="af-form">
            <div class="af-modal-header">
                <h3>Advanced Upload</h3>
                <button type="button" class="btn btn-ghost modal-close">×</button>
            </div>
            
            <div class="af-modal-body">
                <div class="af-upload-grid">
                    <!-- Left: Metadata -->
                    <div class="af-upload-meta">
                        <label class="af-section-label">Metadata Configuration</label>
                        <label>
                            <span>Stream/Group</span>
                            <input type="text" name="stream" placeholder="project-name/v1.0">
                        </label>
                        <label class="af-check-group">
                            <input type="checkbox" name="keep_latest"> 
                            <span>Keep Latest Only</span>
                        </label>
                        <label><span>Tags</span><input type="text" name="tags" placeholder="env=prod, arch=x64"></label>
                        ${ExpirationLabel.LABEL_TEMPLATE}
                    </div>

                    <!-- Right: File Staging -->
                    <div class="af-upload-staging">
                        <label class="af-section-label">Files to Upload</label>
                        
                        <div class="af-drop-zone-mini" id="drop-zone">
                            <input type="file" id="file-input" multiple style="display:none">
                            <span>📥 Drag & Drop or <strong>Click to Browse</strong></span>
                        </div>

                        <div class="af-table-wrapper" style="margin-top:10px; max-height: 250px; overflow-y:auto;">
                            <table class="af-table af-table-compact">
                                <thead>
                                    <tr>
                                        <th>Name</th>
                                        <th style="width:70px">Size</th>
                                        <th style="width:40px"></th>
                                    </tr>
                                </thead>
                                <tbody id="staging-body">
                                    <!-- Files injected here -->
                                </tbody>
                            </table>
                        </div>
                    </div>
                </div>
            </div>

            <div class="af-modal-footer">
                <button type="button" class="btn btn-ghost modal-close">Cancel</button>
                <button type="submit" class="btn btn-primary" id="start-upload-btn" disabled>
                    Start Upload (0 files)
                </button>
            </div>
        </form>
    </dialog>
`;

let stagingList = [];
let path;

export function openUploadDialog(currentPath) {
    if (!document.getElementById('upload-dialog')) {
        document.body.appendChild(template.content.cloneNode(true));
        setupListeners();
    }

    const dialog = document.getElementById('upload-dialog');
    path = currentPath;

    // Clear state on open
    stagingList = [];
    renderStaging(dialog);
    ExpirationLabel.setupExpiryPicker(dialog);
    dialog.showModal();
}

function renderStaging(dialog) {
    const tbody = dialog.querySelector('#staging-body');
    const submitBtn = dialog.querySelector('#start-upload-btn');

    tbody.innerHTML = '';

    stagingList.forEach((file, index) => {
        const row = document.createElement('tr');
        row.innerHTML = `
            <td class="af-file-text" style="max-width: 200px; overflow:hidden; text-overflow:ellipsis; white-space:nowrap;" title="${file.name}">
                ${file.name}
            </td>
            <td class="af-col-mono" style="font-size: 11px;">${Format.formatBytes(file.size)}</td>
            <td>
                <button type="button" class="btn btn-ghost btn-danger remove-file-btn" data-index="${index}">×</button>
            </td>
        `;
        tbody.appendChild(row);
    });

    if (stagingList.length === 0) {
        tbody.innerHTML = `<tr><td colspan="3" class="af-text-muted" style="text-align:center; padding: 20px;">No files added</td></tr>`;
    }

    submitBtn.disabled = stagingList.length === 0;
    submitBtn.textContent = `Start Upload (${stagingList.length} files)`;

    // Attach remove events
    tbody.querySelectorAll('.remove-file-btn').forEach(btn => {
        btn.onclick = () => {
            stagingList.splice(parseInt(btn.dataset.index), 1);
            renderStaging(dialog);
        };
    });
}

function setupListeners() {
    const dialog = document.getElementById('upload-dialog');
    const form = dialog.querySelector('form');
    const dropZone = dialog.querySelector('#drop-zone');
    const fileInput = dialog.querySelector('#file-input');

    const addFiles = (files) => {
        // Append new files to existing list
        stagingList = [...stagingList, ...Array.from(files)];
        renderStaging(dialog);
    };

    dropZone.onclick = () => fileInput.click();
    fileInput.onchange = () => addFiles(fileInput.files);

    // Drag-drop events
    ['dragover', 'dragenter'].forEach(n => {
        dropZone.addEventListener(n, (e) => { e.preventDefault(); dropZone.classList.add('drag-over'); });
    });
    ['dragleave', 'drop'].forEach(n => {
        dropZone.addEventListener(n, (e) => {
            e.preventDefault();
            dropZone.classList.remove('drag-over');
            if (n === 'drop') addFiles(e.dataTransfer.files);
        });
    });

    dialog.querySelectorAll('.modal-close').forEach(btn => {
        btn.onclick = () => dialog.close();
    });

    form.onsubmit = (e) => {
        e.preventDefault();

        const fd = new FormData(form);
        const headers = {
            'X-Stream': fd.get('stream'),
            'X-Tags': fd.get('tags'),
            'X-KeepLatest': fd.get('keep_latest') === 'on' ? 'true' : null,
        };
        const currentExpires = fd.get('expires')?.trim();
        if (currentExpires) {
            headers['X-Expires'] = Format.durationToBackendFormat(currentExpires);
        }

        stagingList.forEach(file => {
            const uploadUrl = `/${path}`.replace(/\/+/g, '/');
            TransferManager.upload(file, uploadUrl, path, headers);
        });

        dialog.close();
    };
}