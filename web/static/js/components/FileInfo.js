import { Format } from '../api/Format.js';

const infoModalTemplate = document.createElement('template');

infoModalTemplate.innerHTML = `
    <dialog class="af-modal" id="file-info-dialog">
        <div class="af-modal-header">
            <h3>File Information</h3>
            <button type="button" class="btn btn-ghost modal-close">×</button>
        </div>
        
        <div class="af-modal-body">
            <div class="af-info-list" style="margin-bottom: 20px;">
                <div class="af-info-item"><label>Name</label><span id="info-name"></span></div>
                <div class="af-info-item"><label>Size</label><span id="info-size"></span></div>
                <div class="af-info-item"><label>Mod time</label><span id="info-modtime"></span></div>
                <div class="af-info-group" style="margin-top: 15px;">
                    <div class="af-info-item">
                        <label>Access Policy</label>
                        <div id="policy-container" style="display: flex; gap: 8px; flex-wrap: wrap; margin-top: 4px;">
                            <!-- Badges injected here -->
                        </div>
                    </div>
                </div>
                </div>

            <div class="af-hash-section">
                <label class="af-section-label">Hashes & Checksums</label>
                
                <div class="af-hash-row">
                    <label>SHA256</label>
                    <div class="af-hash-box">
                        <code class="af-col-mono" id="hash-sha256"></code>
                        <button class="btn btn-ghost copy-btn" data-target="hash-sha256" title="Copy SHA256">📋</button>
                    </div>
                </div>

                <div class="af-hash-row">
                    <label>SHA1</label>
                    <div class="af-hash-box">
                        <code class="af-col-mono" id="hash-sha1"></code>
                        <button class="btn btn-ghost copy-btn" data-target="hash-sha1" title="Copy SHA1">📋</button>
                    </div>
                </div>

                <div class="af-hash-row">
                    <label>MD5</label>
                    <div class="af-hash-box">
                        <code class="af-col-mono" id="hash-md5"></code>
                        <button class="btn btn-ghost copy-btn" data-target="hash-md5" title="Copy MD5">📋</button>
                    </div>
                </div>
            </div>
        </div>

        <div class="af-modal-footer">
            <button type="button" class="btn btn-primary modal-close">Close</button>
        </div>
    </dialog>
`;

function renderPolicyBadges(policy, container) {
    container.innerHTML = '';

    if (policy.is_immutable) {
        container.innerHTML += `<span class="badge badge-policy-immutable" title="Metadata lock is active">🔒 Immutable</span>`;
    }

    if (policy.is_protected) {
        container.innerHTML += `<span class="badge badge-policy-protected" title="Directory is append-only in config">🛡️ System Protected</span>`;
    }

    if (!policy.is_allowed) {
        container.innerHTML += `<span class="badge badge-policy-restricted" title="Your token lacks permission for this path">🚫 Restricted Scope</span>`;
    }

    if (!policy.is_immutable && !policy.is_protected && policy.is_allowed) {
        container.innerHTML = `<span class="af-text-muted" style="font-size: 12px;">Full Access</span>`;
    }
}

export function openFileInfo(file) {
    if (!document.getElementById('file-info-dialog')) {
        document.body.appendChild(infoModalTemplate.content.cloneNode(true));
        setupInfoListeners();
    }

    const dialog = document.getElementById('file-info-dialog');

    // Fill basic info
    dialog.querySelector('#info-name').textContent = file.name;
    dialog.querySelector('#info-size').textContent = file.size;
    dialog.querySelector('#info-modtime').textContent = Format.dateTime(file.modtime);
    renderPolicyBadges(file.policy, dialog.querySelector("#policy-container"));

    // Fill hashes (handling potential missing data)
    dialog.querySelector('#hash-sha256').textContent = file.checksum_sha256 || 'N/A';
    dialog.querySelector('#hash-sha1').textContent = file.checksum_sha1 || 'N/A';
    dialog.querySelector('#hash-md5').textContent = file.checksum_md5 || 'N/A';
    dialog.showModal();
}

function setupInfoListeners() {
    const dialog = document.getElementById('file-info-dialog');

    dialog.querySelectorAll('.modal-close').forEach(btn => {
        btn.onclick = () => dialog.close();
    });

    // Copy to Clipboard logic
    dialog.querySelectorAll('.copy-btn').forEach(btn => {
        btn.onclick = async () => {
            const targetId = btn.dataset.target;
            const text = document.getElementById(targetId).textContent;

            if (text === 'N/A') return;

            try {
                await navigator.clipboard.writeText(text);

                // Visual feedback
                const originalText = btn.textContent;
                btn.textContent = '✅';
                btn.classList.add('btn-success');

                setTimeout(() => {
                    btn.textContent = originalText;
                    btn.classList.remove('btn-success');
                }, 1500);
            } catch (err) {
                console.error('Failed to copy!', err);
            }
        };
    });
}