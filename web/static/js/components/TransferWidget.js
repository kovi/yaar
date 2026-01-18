// static/js/components/TransferWidget.js

const template = document.createElement('template');
template.innerHTML = `
    <div id="transfer-container" class="af-transfer-widget af-card hidden">
        <div class="af-card-header">
            <span>Uploads</span>
            <button id="transfer-minimize" class="btn btn-ghost">_</button>
        </div>
        <div id="transfer-list" class="af-transfer-list"></div>
    </div>
`;

const rowTemplate = document.createElement('template');
rowTemplate.innerHTML = `
    <div class="af-transfer-item">
        <div class="af-transfer-meta">
            <div style="display: flex; flex-direction: row; overflow: hidden; width:100%">
                <span class="file-name" style="flex-grow: 1; font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;"></span>
                <button class="btn btn-ghost action-btn" style="padding: 2px 6px; font-size: 11px; justify-content: flex-end"></button>
            </div>
        </div>
        <div style="display:flex; flex-direction:row;">
            <div class="af-progress-track" style="flex-grow:1">
                <div class="af-progress-fill"></div>
            </div>
            <span class="status-text badge"></span>
        </div>
        <div style="display: flex; justify-content: flex-end;">
        </div>
    </div>
`;

class TransferWidget {
    constructor() {
        const clone = template.content.cloneNode(true);
        this.container = clone.getElementById('transfer-container');
        this.list = clone.getElementById('transfer-list');

        // Minimize toggle
        clone.getElementById('transfer-minimize').onclick = () => {
            this.list.classList.toggle('minimized');
        };

        document.body.appendChild(clone);
    }

    /**
     * Adds or updates a row in the UI
     */
    renderTask(task, onCancel) {
        this.container.classList.remove('hidden');

        let row = this.list.querySelector(`[data-id="${task.id}"]`);
        if (!row) {
            const rowClone = rowTemplate.content.cloneNode(true);
            row = rowClone.querySelector('.af-transfer-item');
            row.dataset.id = task.id;
            this.list.prepend(row); // Newest on top
        }

        row.className = `af-transfer-item status-${task.status}`;
        row.querySelector('.file-name').textContent = task.name;

        const time = () => task.finishedAt ? new Date(task.finishedAt).toLocaleString() : 'unknown time';

        if (task.status === 'error' || task.status === 'cancelled') {
            const reason = task.errorReason || 'No specific reason provided';

            // Combine info into a descriptive tooltip
            row.title = `Path: ${task.path}\nStatus: ${task.status.toUpperCase()}\nReason: ${reason}\nTime: ${time()}`;
        } else if (task.status === 'completed') {
            row.title = `Completed at: ${time()}\nPath: ${task.path}`;
            row.querySelector('.status-text').textContent = `${task.percent}%`;
        } else {
            row.title = `Uploading to: ${task.path}`;
        }
        row.querySelector('.af-progress-fill').style.width = `${task.percent}%`;
        row.querySelector('.status-text').textContent = `${task.percent}%`;

        const actionBtn = row.querySelector('.action-btn');

        if (task.status === 'uploading') {
            actionBtn.textContent = 'Cancel';
            actionBtn.onclick = onCancel;
        } else {
            actionBtn.textContent = 'Clear';
            actionBtn.onclick = () => {
                row.remove();
                if (this.list.children.length === 0) this.container.classList.add('hidden');
                window.dispatchEvent(new CustomEvent('transfer:dismiss', { detail: task.id }));
            };
        }
    }
}

// Export a single instance (Singleton pattern)
export const ui = new TransferWidget();