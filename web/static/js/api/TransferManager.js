import { ui } from '../components/TransferWidget.js';
import { uuid } from '../api/Crypto.js'
const STORAGE_KEY = 'artifactory_history';

export const TransferManager = {
    init() {
        // Load history from storage
        const history = JSON.parse(localStorage.getItem(STORAGE_KEY) || '[]');

        // Render old items (marked as finished or interrupted)
        history.forEach(task => ui.renderTask(task));

        // Listen for UI dismissals
        window.addEventListener('transfer:dismiss', (e) => {
            const history = JSON.parse(localStorage.getItem(STORAGE_KEY) || '[]');
            const filtered = history.filter(t => t.id !== e.detail);
            localStorage.setItem(STORAGE_KEY, JSON.stringify(filtered));
        });
    },

    upload(file, url, targetPath, customHeaders = {}) {
        console.log("upload", file, url, targetPath, customHeaders)
        const task = {
            id: uuid(),
            name: file.name,
            status: 'uploading',
            percent: 0,
            path: targetPath,
            errorReason: null,
            finishedAt: null
        };

        const xhr = new XMLHttpRequest();

        const saveHistory = (task) => {
            task.finishedAt = new Date().toISOString();

            const history = JSON.parse(localStorage.getItem(STORAGE_KEY) || '[]');
            history.push(task);
            localStorage.setItem(STORAGE_KEY, JSON.stringify(history));
        }

        const handleFailure = (reason) => {
            task.status = 'error';
            task.errorReason = reason;

            saveHistory(task);
            ui.renderTask(task);
        }

        xhr.onerror = () => handleFailure("Network connection failed");
        xhr.onabort = () => handleFailure("Upload cancelled by user");

        xhr.upload.onprogress = (e) => {
            if (e.lengthComputable) {
                task.percent = Math.round((e.loaded / e.total) * 100);
                ui.renderTask(task, () => xhr.abort());
            }
        };

        xhr.onload = () => {
            const success = xhr.status >= 200 && xhr.status < 300;
            if (success) {
                task.status = success ? 'completed' : 'error';
                task.percent = 100;
                saveHistory(task);
                ui.renderTask(task);
                window.dispatchEvent(new CustomEvent('artifactory:refresh', { detail: { path: targetPath } }));
            } else {
                // Try to extract JSON error message from the server
                let message = `Server returned ${xhr.status}`;
                try {
                    const resp = JSON.parse(xhr.responseText);
                    message = resp.error || message;
                } catch (e) { /* fallback to status code */ }

                handleFailure(message);
            }
        };

        const u = `${url}/${file.name}`.replace(/\/+/g, '/');
        xhr.open('PUT', u);
        for (const [key, value] of Object.entries(customHeaders)) {
            if (value !== undefined && value !== null && value !== '') {
                xhr.setRequestHeader(key, value);
            }
        }

        const token = localStorage.getItem('af_token');
        if (token) {
            xhr.setRequestHeader('Authorization', `Bearer ${token}`);
        }

        xhr.send(file);
        ui.renderTask(task, () => xhr.abort());
    }
};