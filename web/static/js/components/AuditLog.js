import { API } from "../api/ApiClient.js";
import { Format } from "../api/Format.js";
import { showToast } from "./Toast.js";

const FIXED_KEYS = new Set([
	"time",
	"level",
	"msg",
	"action",
	"resource",
	"status",
	"user",
	"token_name",
	"ip",
]);

const ACTION_COLORS = {
	FILE_UPLOAD: "badge-info",
	FILE_DELETE: "badge-danger",
	FILE_RENAME: "badge-warning",
	FILE_MOVE: "badge-warning",
	DIR_CREATE: "badge-success",
	META_PATCH: "badge-outline",
	USER_UPDATE: "badge-purple",
	USER_DELETE: "badge-danger",
	TOKEN_CREATED: "badge-teal",
	SYSTEM_SYNC_CLEANUP: "badge-outline",
};

function actionBadge(action) {
	const cls = ACTION_COLORS[action] || "badge-outline";
	return `<span class="badge ${cls} af-audit-action">${Format.escapeHtml(action)}</span>`;
}

function statusBadge(status) {
	if (!status) return "";
	const cls = status === "SUCCESS" ? "badge-success" : "badge-danger";
	return `<span class="badge ${cls}">${Format.escapeHtml(status)}</span>`;
}

// The single most human-readable summary of what actually happened.
// Prefer an explicit "reason", otherwise fall back to the first informative extra.
function detailSummary(entry) {
	if (entry.reason != null && entry.reason !== "") return String(entry.reason);
	const extras = Object.entries(entry).filter(([k]) => !FIXED_KEYS.has(k));
	for (const [k, v] of extras) {
		if (v == null || typeof v === "object") continue;
		const val = String(v);
		if (val !== "") return `${k}: ${val}`;
	}
	return "";
}

function renderExtras(entry) {
	const extras = Object.entries(entry).filter(([k]) => !FIXED_KEYS.has(k));
	if (extras.length === 0)
		return '<em style="color:var(--text-muted)">No extras</em>';
	return extras
		.map(([k, v]) => {
			const val = typeof v === "object" ? JSON.stringify(v) : String(v);
			// Both the key and the value originate from audit entries whose fields
			// (resource paths, filenames, tag values) are attacker-controlled.
			return `<div class="af-audit-extra-row"><span class="af-audit-extra-key">${Format.escapeHtml(k)}</span><span class="af-audit-extra-val">${Format.escapeHtml(val)}</span></div>`;
		})
		.join("");
}

function renderRows(entries, tbody) {
	for (const entry of entries) {
		const tr = document.createElement("tr");
		tr.className = "af-audit-entry-row";
		// Every interpolated field below comes from an audit entry, and audit
		// entries record attacker-supplied data verbatim: `resource` is the
		// uploaded path, and the extras carry filenames and tag values. Anyone
		// who can write a single path can therefore plant markup that renders in
		// an *admin's* browser, so all of it is escaped.
		const userCell = entry.token_name
			? `${Format.escapeHtml(entry.user ?? "")}<br><span class="af-audit-token-name" title="API token">${Format.escapeHtml(entry.token_name)}</span>`
			: Format.escapeHtml(entry.user ?? "");
		const detail = Format.escapeHtml(detailSummary(entry));
		tr.innerHTML = `
			<td class="af-col-mono" style="white-space:nowrap;font-size:11px">${Format.escapeHtml(Format.dateTime(entry.time))}</td>
			<td>${userCell}</td>
			<td>${actionBadge(entry.action ?? "")}</td>
			<td class="af-audit-detail" title="${detail}">${detail}</td>
			<td class="af-audit-resource">${Format.escapeHtml(entry.resource ?? "")}</td>
			<td>${statusBadge(entry.status)}</td>
			<td style="font-size:11px;color:var(--text-muted)">${Format.escapeHtml(entry.ip ?? "")}</td>
		`;

		const extrasTr = document.createElement("tr");
		extrasTr.className = "af-audit-extras-row";
		extrasTr.innerHTML = `<td colspan="7"><div class="af-audit-extras">${renderExtras(entry)}</div></td>`;

		tr.addEventListener("click", () => {
			const open = extrasTr.classList.toggle("af-audit-extras-open");
			tr.classList.toggle("af-audit-row-expanded", open);
		});

		tbody.appendChild(tr);
		tbody.appendChild(extrasTr);
	}
}

export async function openAuditLog() {
	const existingDialog = document.getElementById("af-audit-dialog");
	if (existingDialog) {
		existingDialog.showModal();
		return;
	}

	const dialog = document.createElement("dialog");
	dialog.id = "af-audit-dialog";
	dialog.className = "af-modal af-audit-dialog";
	dialog.innerHTML = `
		<div class="af-modal-header">
			<h3>Audit Log</h3>
			<button type="button" class="btn btn-ghost modal-close" style="font-size:18px;padding:4px 8px">×</button>
		</div>
		<div class="af-audit-toolbar">
			<input type="search" id="af-audit-filter" placeholder="Filter entries…" autocomplete="off" />
			<div id="af-audit-warning" class="af-audit-warning hidden">
				Scan limit reached — results may be incomplete. Use a more specific filter.
			</div>
		</div>
		<div class="af-modal-body" style="padding:0;overflow-y:auto">
			<div class="af-table-wrapper" style="border:none;border-radius:0;box-shadow:none">
				<table class="af-table af-table-compact af-audit-table">
					<thead>
						<tr>
							<th>Time</th>
							<th>User</th>
							<th>Action</th>
							<th>Detail</th>
							<th>Resource</th>
							<th>Status</th>
							<th>IP</th>
						</tr>
					</thead>
					<tbody id="af-audit-tbody"></tbody>
				</table>
			</div>
		</div>
		<div class="af-modal-footer" id="af-audit-footer">
			<span id="af-audit-count" style="font-size:12px;color:var(--text-muted);margin-right:auto"></span>
			<button type="button" class="btn btn-ghost btn-sm" id="af-audit-load-more" style="display:none">Load older</button>
			<button type="button" class="btn btn-ghost btn-sm" id="af-audit-refresh">Refresh</button>
			<button type="button" class="btn btn-primary modal-close">Close</button>
		</div>
	`;
	document.body.appendChild(dialog);

	dialog.querySelectorAll(".modal-close").forEach((b) => {
		b.onclick = () => dialog.close();
	});

	let nextBeforeOffset = -1;
	// Which rotated audit file the next page continues in; 0 is the live log.
	let nextGeneration = 0;
	let totalLoaded = 0;
	let currentFilter = "";
	let loading = false;

	const tbody = dialog.querySelector("#af-audit-tbody");
	const loadMoreBtn = dialog.querySelector("#af-audit-load-more");
	const refreshBtn = dialog.querySelector("#af-audit-refresh");
	const countEl = dialog.querySelector("#af-audit-count");
	const warningEl = dialog.querySelector("#af-audit-warning");
	const filterInput = dialog.querySelector("#af-audit-filter");

	async function load(reset) {
		if (loading) return;
		loading = true;
		loadMoreBtn.disabled = true;
		refreshBtn.disabled = true;

		try {
			const page = await API.getAuditLog({
				beforeOffset: reset ? -1 : nextBeforeOffset,
				generation: reset ? 0 : nextGeneration,
				filter: currentFilter,
			});

			if (reset) {
				tbody.innerHTML = "";
				totalLoaded = 0;
			}

			renderRows(page.entries, tbody);
			totalLoaded += page.entries.length;
			nextBeforeOffset = page.next_before_offset;
			nextGeneration = page.next_generation ?? 0;

			countEl.textContent = totalLoaded
				? `Showing ${totalLoaded} entr${totalLoaded === 1 ? "y" : "ies"}`
				: "No entries found";

			warningEl.classList.toggle("hidden", !page.scan_limit_hit);
			loadMoreBtn.style.display = page.has_more ? "inline-flex" : "none";
		} catch (err) {
			showToast(err.message, "error");
		} finally {
			loading = false;
			loadMoreBtn.disabled = false;
			refreshBtn.disabled = false;
		}
	}

	loadMoreBtn.onclick = () => load(false);
	refreshBtn.onclick = () => load(true);

	let debounceTimer;
	filterInput.addEventListener("input", () => {
		clearTimeout(debounceTimer);
		debounceTimer = setTimeout(() => {
			currentFilter = filterInput.value.trim();
			load(true);
		}, 400);
	});

	await load(true);
	dialog.showModal();
}
