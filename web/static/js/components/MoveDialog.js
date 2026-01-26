import { API } from '../api/ApiClient.js';
import { pickFolder } from './FolderPicker.js';

export function openMoveDialog(file, currentPath) {
    const dialog = document.createElement('dialog');
    dialog.className = 'af-modal';
    const oldFullPath = (currentPath + '/' + file.name).replace(/\/+/g, '/');

    dialog.innerHTML = `
        <form method="dialog" class="af-form">
            <div class="af-modal-header">
                <h3>Move 📦 ${file.name}</h3>
                <button type="button" class="btn btn-ghost modal-close">×</button>
            </div>
            <div class="af-modal-body">
                <label>
                    <span>Destination Folder</span>
                    <div class="af-input-with-action">
                        <input type="text" name="dest_folder" id="dest-folder-input" value="${currentPath}" required>
                        <button type="button" class="btn btn-ghost" id="browse-folders-btn" title="Browse Folders">📂</button>
                    </div>
                </label>
                <label style="margin-top: 15px;">
                    <span>New Filename</span>
                    <input type="text" name="new_name" value="${file.name}" required>
                </label>
            </div>
            <div class="af-modal-footer">
                <button type="button" class="btn btn-ghost modal-close">Cancel</button>
                <button type="submit" class="btn btn-primary">Execute Move</button>
            </div>
        </form>
    `;

    document.body.appendChild(dialog);
    dialog.showModal();

    // --- Visual Browser Trigger ---
    dialog.querySelector('#browse-folders-btn').onclick = async () => {
        const selectedFolder = await pickFolder(currentPath);
        if (selectedFolder) {
            dialog.querySelector('#dest-folder-input').value = selectedFolder;
        }
    };

    const form = dialog.querySelector('form');
    form.onsubmit = async (e) => {
        e.preventDefault();
        const fd = new FormData(form);
        const targetPath = (fd.get('dest_folder') + '/' + fd.get('new_name')).replace(/\/+/g, '/');
        
        try {
            await API.moveResource(oldFullPath, targetPath);
            dialog.close();
            dialog.remove();
            window.dispatchEvent(new CustomEvent('artifactory:refresh', { detail: { path: currentPath } }));
        } catch (err) {
            alert(err.message);
        }
    };

    dialog.querySelectorAll('.modal-close').forEach(btn => btn.onclick = () => {
        dialog.close();
        dialog.remove();
    });
}