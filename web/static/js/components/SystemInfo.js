import { API } from '../api/ApiClient.js';

const template = document.createElement('template');
template.innerHTML = `
    <div class="af-action-banner">
        <span class="af-action-banner-text">Synchronize metadata with the physical filesystem.</span>
        <button class="btn btn-ghost" id="btn-rescan" style="border-color: var(--border)">
            🔄 Re-scan Now
        </button>
    </div>

    <div class="af-info-list" style="margin-bottom: 20px;">
        <div class="af-info-item">
            <label>Git Version</label>
            <span id="settings-version" class="af-col-mono"></span>
        </div>
        <div class="af-info-item">
            <label>Build Date</label>
            <span id="settings-build-date" class="af-col-mono"></span>
        </div>
        <div class="af-info-item">
            <label>Runtime</label>
            <span id="settings-runtime" class="af-col-mono"></span>
        </div>
        <div class="af-info-item">
            <label>Active Configuration</label>
            <div class="af-config-viewer">
                <pre id="settings-config-json"></pre>
            </div>
        </div>
        <div class="af-info-item">
            <label>Dependencies</label>
            <div class="af-config-viewer">
                <pre id="settings-deps"></pre>
            </div>
        </div>
    </div>
`;

export async function SystemInfo() {
    const container = template.content.cloneNode(true);
    const data = await API.getSettings();

    container.querySelector('#settings-version').textContent = data.version;
    if (data.is_dirty) {
        const verEl = container.querySelector('#settings-version');
        verEl.innerHTML += ` <span class="badge badge-danger" title="Uncommitted changes during build">DIRTY</span>`;
    }
    container.querySelector('#settings-build-date').textContent = data.build_date;

    // Fill runtime info
    container.querySelector('#settings-runtime').textContent =
        `${data.runtime.os}/${data.runtime.arch} (CGO: ${data.runtime.cgo})`;

    // Fill Dependencies
    const depList = Object.entries(data.dependencies)
        .map(([name, ver]) => `${name} @ ${ver}`)
        .join('\n');
    container.querySelector('#settings-deps').textContent = depList;

    // Fill Config JSON 
    container.querySelector('#settings-config-json').textContent = JSON.stringify(data.config, null, 2);

    const syncBtn = container.querySelector('#btn-rescan');
    syncBtn.onclick = async () => {
        const res = await API.triggerSync();
        alert(JSON.stringify(res));
    };

    return container;
}
