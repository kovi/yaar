import { API } from "../api/ApiClient.js";
import { Format } from "../api/Format.js";
import { showToast } from "./Toast.js";

const template = document.createElement("template");
template.innerHTML = `
    <div class="af-system-info">
        <!-- 1. VERSION & UPTIME -->
        <div class="af-card af-version-card">
            <div class="af-info-group">
                <div class="af-info-item">
                    <label>Version</label>
                    <div id="ver-string" class="af-version-row"></div>
                </div>
                <div class="af-info-item">
                    <label>Build Date</label>
                    <span id="ver-date" class="af-col-mono"></span>
                </div>
                <div class="af-info-item">
                    <label>Status</label>
                    <span id="ver-uptime" class="badge badge-success"></span>
                </div>
                <div class="af-info-item">
                    <label>Runtime Environment</label>
                    <span id="ver-runtime"></span>
                </div>
                <div class="af-info-item">
                    <label>Commit Hash</label>
                    <code id="ver-commit" class="af-col-mono" style="font-size: 11px; color: var(--text-muted)"></code>
                </div>
            </div>
        </div>

        <!-- 2. EXPANDED HEALTH METRICS -->
        <div class="af-metric-grid">
            <!-- Storage -->
            <div class="af-metric-card">
                <label>Storage Volume</label>
                <div class="af-metric-value" id="m-storage-val"></div>
                <div class="af-progress-mini"><div class="af-progress-mini-fill" id="m-storage-bar"></div></div>
                <div class="af-metric-sub" id="m-storage-sub"></div>
            </div>
            
            <!-- Database -->
            <div class="af-metric-card">
                <label>Database Size</label>
                <div class="af-metric-value" id="m-db-val"></div>
                <div class="af-metric-sub" style="margin-top:12px">SQLite file on disk</div>
            </div>

            <!-- RAM -->
            <div class="af-metric-card">
                <label>Memory (RAM)</label>
                <div class="af-metric-value" id="m-ram-val"></div>
                <div class="af-progress-mini"><div class="af-progress-mini-fill" id="m-ram-bar"></div></div>
                <div class="af-metric-sub" id="m-ram-sub"></div>
            </div>

            <!-- Goroutines -->
            <div class="af-metric-card">
                <label>System Load</label>
                <div class="af-metric-value" id="m-load-val"></div>
                <div class="af-metric-sub" style="margin-top:12px">Active GoRoutines</div>
            </div>
        </div>

        <!-- 3. STATE TABLE -->
        <label class="af-section-label">Persistent System State</label>
        <div class="af-table-wrapper" style="margin-bottom: 24px;">
            <table class="af-table af-table-compact">
                <thead><tr><th>Parameter</th><th>Value</th><th>Last Sync</th></tr></thead>
                <tbody id="state-table-body"></tbody>
            </table>
        </div>

        <!-- 4. OPERATIONS -->
        <div class="af-operations-box" id="ops-container">
            <div class="af-operations-header">Operations</div>
            <div class="af-operation-item">
                <div class="af-operation-info">
                    <h4>Reconcile Filesystem</h4>
                    <p>Sync physical files with DB.</p>
                </div>
                <button class="btn btn-ghost btn-sm" id="btn-rescan">🔄 Run Sync</button>
            </div>
            <div class="af-operation-item">
                <div class="af-operation-info">
                    <h4>Optimize SQLite</h4>
                    <p>Runs automatically on a schedule. Reclaim space now via VACUUM.</p>
                </div>
                <button class="btn btn-ghost btn-sm" id="btn-vacuum">🧹 Run Vacuum</button>
            </div>
        </div>

        <!-- 4b. JANITOR FAILURES -->
        <div class="af-card" id="janitor-failures-card" style="margin-top: 24px; display: none;">
            <label class="af-section-label" style="color: var(--danger); margin-top: 0;">Janitor Cleanup Failures</label>
            <div class="af-table-wrapper">
                <table class="af-table af-table-compact af-janitor-table">
                    <thead><tr><th>Type</th><th>Identifier</th><th>Attempts</th><th>Last Error</th><th>Next Retry</th><th>Actions</th></tr></thead>
                    <tbody id="janitor-failures-body"></tbody>
                </table>
            </div>
        </div>
        
        <!-- 5. CONFIG -->
        <div style="margin-top: 32px">
            <label class="af-section-label">Active Configuration</label>
            <div class="af-config-viewer"><pre id="config-json"></pre></div>
        </div>
    </div>
`;

export async function SystemInfo() {
	const data = await API.getSettings();
	const container = template.content.cloneNode(true);

	// 1. Populate Version & Uptime
	container.getElementById("ver-string").innerHTML = `
        <span class="af-ver-text">${data.version}</span>
        ${data.is_dirty ? '<span class="badge badge-danger af-badge-tiny">DIRTY</span>' : ""}
    `;
	container.getElementById("ver-date").textContent = Format.dateTime(
		data.build_date,
	);
	container.getElementById("ver-uptime").textContent =
		`Online for ${Format.duration(data.uptime_seconds)}`;
	container.getElementById("ver-runtime").textContent =
		`${data.runtime.os}/${data.runtime.arch} (${data.go_version})`;
	container.getElementById("ver-commit").textContent = data.commit || "—";

	// 2. Populate Metrics
	// Storage
	const sUsed = data.storage.used;
	const sPct = Math.round((sUsed / data.storage.total) * 100) || 0;
	container.getElementById("m-storage-val").textContent =
		`${Format.formatBytes(sUsed)} / ${Format.formatBytes(data.storage.total)}`;
	container.getElementById("m-storage-bar").style.width = `${sPct}%`;
	container.getElementById("m-storage-sub").textContent =
		`${sPct}% capacity used`;

	// Database
	container.getElementById("m-db-val").textContent = Format.formatBytes(
		data.db_size,
	);

	// RAM (We use a dynamic max of 'sys_total')
	const rAlloc = data.runtime.mem_alloc;
	const rTotal = data.runtime.sys_total;
	const rPct = Math.round((rAlloc / rTotal) * 100) || 0;
	container.getElementById("m-ram-val").textContent =
		Format.formatBytes(rAlloc);
	container.getElementById("m-ram-bar").style.width = `${rPct}%`;
	container.getElementById("m-ram-bar").style.backgroundColor =
		rPct > 80 ? "var(--warning)" : "var(--primary)";
	container.getElementById("m-ram-sub").textContent =
		`System allocated: ${Format.formatBytes(rTotal)}`;

	// Load
	container.getElementById("m-load-val").textContent = data.runtime.goroutines;

	// 3. State Table
	// system_states and config are admin-only fields; a non-admin gets the
	// operational metrics above but not these, so both sections degrade to a
	// placeholder rather than throwing on undefined.
	const tbody = container.getElementById("state-table-body");
	tbody.innerHTML =
		(data.system_states || [])
			.map(
				(s) => `
        <tr>
            <td><code>${s.key}</code></td>
            <td>${s.value}</td>
            <td class="af-col-mono" style="font-size:11px">${Format.dateTime(s.updated_at)}</td>
        </tr>
    `,
			)
			.join("") ||
		'<tr><td colspan="3" class="af-text-muted">No state records.</td></tr>';

	// 4. Config
	container.getElementById("config-json").textContent = data.config
		? JSON.stringify(data.config, null, 2)
		: "Administrator access required.";

	// 4b. Janitor Failures
	try {
		const failures = await API.getJanitorErrors();
		if (failures && failures.length > 0) {
			const card = container.getElementById("janitor-failures-card");
			card.style.display = "block";
			const tbodyFailures = container.getElementById("janitor-failures-body");
			tbodyFailures.innerHTML = failures
				.map(
					(f) => `
                <tr>
                    <td><span class="badge ${f.type === "group" ? "badge-info" : "badge-outline"}">${Format.escapeHtml(f.type)}</span></td>
                    <td class="af-janitor-key"><code title="${Format.escapeHtml(f.key)}">${Format.escapeHtml(f.key)}</code></td>
                    <td style="text-align:center">${f.attempts}</td>
                    <td class="af-janitor-error" title="${Format.escapeHtml(f.last_error)}">${Format.escapeHtml(f.last_error)}</td>
                    <td class="af-col-mono" style="font-size:11px;white-space:nowrap">${Format.dateTime(f.retry_after)}</td>
                    <td><button class="btn btn-ghost btn-sm btn-clear-janitor" data-key="${Format.escapeHtml(f.key)}">Clear</button></td>
                </tr>
            `,
				)
				.join("");

			// Wire up clear buttons
			tbodyFailures.querySelectorAll(".btn-clear-janitor").forEach((btn) => {
				btn.onclick = async () => {
					btn.disabled = true;
					btn.innerText = "Clearing...";
					try {
						const res = await API.clearJanitorError(btn.dataset.key);
						showToast(res.message, "success");
						// Refresh the UI to reflect cleared error
						window.dispatchEvent(new CustomEvent("af:settings-refresh"));
					} catch (err) {
						showToast(err.message, "error");
						btn.disabled = false;
						btn.innerText = "Clear";
					}
				};
			});
		}
	} catch (e) {
		console.error("Failed to load janitor errors:", e);
	}

	// Listeners
	const rescanBtn = container.getElementById("btn-rescan");

	rescanBtn.onclick = async () => {
		rescanBtn.disabled = true;
		try {
			await API.triggerSync();
			showToast("Re-scan triggered in background", "info");
		} catch (e) {
			showToast(e.message, "error");
		} finally {
			rescanBtn.disabled = false;
		}
	};

	const vacuumBtn = container.getElementById("btn-vacuum");
	vacuumBtn.onclick = async () => {
		if (!confirm("Vacuuming may briefly lock the database. Proceed?")) return;
		vacuumBtn.disabled = true;
		const originalText = vacuumBtn.innerHTML;
		vacuumBtn.innerHTML = "⏳ Busy...";
		try {
			const res = await API.vacuumDatabase();
			showToast(res.message, "success");
			// Optionally refresh the view to show new DB size
			window.dispatchEvent(new CustomEvent("af:settings-refresh"));
		} catch (e) {
			showToast(e.message, "error");
		} finally {
			vacuumBtn.innerHTML = originalText;
			vacuumBtn.disabled = false;
		}
	};

	return container;
}
