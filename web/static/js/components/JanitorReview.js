import { API } from "../api/ApiClient.js";
import { Format } from "../api/Format.js";
import { showToast } from "./Toast.js";

// Janitor status, suspend/resume controls and pending-deletion review.
//
// After a migration that changed retention data the server suspends the
// janitor. Here an admin sees what it would delete, keeps (locks) anything
// that must survive, and resumes it. Admins can also suspend it at any time.

const REASON_LABEL = {
	due: { text: "Due now", cls: "badge-danger" },
	next_upload: { text: "On next change", cls: "badge-warning" },
};

const esc = (s) => Format.escapeHtml(String(s ?? ""));

// Shown on the Keep controls: locking is what keeps a file, and it also
// refuses overwrites, which a CI job re-uploading the same path would hit.
const LOCK_NOTE =
	"Kept files are locked: uploads that overwrite the same path are rejected (403) until you unlock them in the file browser.";

export function isMigrationSuspension(status) {
	return !!status?.suspended && /^migration:/.test(status.reason || "");
}

// One-line status: badge, reason and since.
export function janitorStatusHtml(status) {
	if (!status.suspended) {
		return `<span class="badge badge-success">Running</span>`;
	}
	return `<span class="badge badge-warning">Suspended</span>
		<span class="af-jr-reason">${esc(status.reason)}</span>
		<span class="af-text-muted af-jr-since">since ${Format.dateTime(status.since)}</span>`;
}

// Suspend / resume controls, shared by the Janitor tab and System → Operations.
// getSummary() describes what resuming will delete, for the confirmation.
export function janitorControls(status, { getSummary } = {}) {
	const box = document.createElement("div");
	box.className = "af-jr-controls";

	if (status.suspended) {
		const resumeBtn = document.createElement("button");
		resumeBtn.type = "button";
		resumeBtn.className = "btn btn-sm btn-primary";
		resumeBtn.textContent = isMigrationSuspension(status)
			? "Approve & resume janitor"
			: "Resume janitor";
		box.appendChild(resumeBtn);

		resumeBtn.onclick = async () => {
			const summary = getSummary?.() || "everything that has expired";
			if (
				!confirm(
					`Resume the janitor? It will delete ${summary} within about 30 seconds.`,
				)
			)
				return;
			resumeBtn.disabled = true;
			try {
				await API.resumeJanitor();
				showToast("Janitor resumed", "success");
				refresh();
			} catch (e) {
				showToast(e.message, "error");
				resumeBtn.disabled = false;
			}
		};
		return box;
	}

	const suspendBtn = document.createElement("button");
	suspendBtn.type = "button";
	suspendBtn.className = "btn btn-sm btn-ghost";
	suspendBtn.textContent = "Suspend janitor";
	box.appendChild(suspendBtn);

	// Inline form instead of prompt(): the reason is shown to every admin
	// (status, bell alert, audit log) so the next person knows why.
	const form = document.createElement("form");
	form.className = "af-jr-suspend-form";
	form.hidden = true;
	form.innerHTML = `
		<input type="text" class="af-jr-filter af-jr-suspend-reason" maxlength="200"
			placeholder="Reason (optional), e.g. release freeze, storage migration">
		<button type="submit" class="btn btn-sm btn-primary">Suspend</button>
		<button type="button" class="btn btn-sm btn-ghost af-jr-suspend-cancel">Cancel</button>`;
	box.appendChild(form);
	const reasonInput = form.querySelector(".af-jr-suspend-reason");

	suspendBtn.onclick = () => {
		form.hidden = false;
		suspendBtn.hidden = true;
		reasonInput.focus();
	};
	form.querySelector(".af-jr-suspend-cancel").onclick = () => {
		form.hidden = true;
		suspendBtn.hidden = false;
	};
	form.onsubmit = async (e) => {
		e.preventDefault();
		const submit = form.querySelector('button[type="submit"]');
		submit.disabled = true;
		try {
			await API.suspendJanitor(reasonInput.value.trim());
			showToast("Janitor suspended", "info");
			refresh();
		} catch (err) {
			showToast(err.message, "error");
			submit.disabled = false;
		}
	};
	return box;
}

// Status card with controls and the pending-deletion review (Janitor tab).
export async function JanitorReview(status) {
	const card = document.createElement("div");
	card.className = "af-card af-janitor-review";
	card.id = "janitor-review-card";

	card.innerHTML = `
		<div class="af-jr-head">
			<div>
				<label class="af-section-label" style="margin-top:0">Status</label>
				<div class="af-jr-status"></div>
			</div>
		</div>
		<div class="af-jr-body"></div>
	`;

	const statusEl = card.querySelector(".af-jr-status");
	const body = card.querySelector(".af-jr-body");

	let hint;
	if (!status.suspended) {
		hint =
			"Deletes expired files every 30 seconds. Suspend it to stop all deletions until you resume it.";
	} else if (isMigrationSuspension(status)) {
		hint =
			"Nothing is deleted while suspended. Review the pending deletions below, keep what must survive, then resume the janitor.";
	} else {
		hint =
			"Nothing is deleted while suspended. Expired files pile up and are deleted once the janitor is resumed.";
	}
	statusEl.innerHTML = `${janitorStatusHtml(status)}<p class="af-jr-hint af-text-muted">${hint}</p>`;
	if (status.suspended) card.classList.add("af-jr-suspended");

	card
		.querySelector(".af-jr-head")
		.appendChild(
			janitorControls(status, { getSummary: () => card.dataset.summary }),
		);

	// Pending deletions are always listed on this tab: it's where the admin
	// comes to see what the janitor is about to do.
	await renderPending(card, body);

	return card;
}

function refresh() {
	window.dispatchEvent(new CustomEvent("af:settings-refresh"));
	window.dispatchEvent(new CustomEvent("af:notifications-refresh"));
}

async function renderPending(card, body) {
	body.innerHTML = `<div class="af-text-muted">Loading pending deletions…</div>`;
	let p;
	try {
		p = await API.getPendingDeletions();
	} catch (e) {
		body.innerHTML = `<div class="af-text-muted">Failed to load: ${esc(e.message)}</div>`;
		return;
	}

	const groups = p.groups || [];
	const files = p.files || [];
	const due = groups.filter((g) => g.reason === "due");
	const nextUpload = groups.filter((g) => g.reason === "next_upload");
	const dueFiles = due.reduce((n, g) => n + g.files.length, 0) + files.length;
	const dueSize =
		due.reduce((n, g) => n + g.size, 0) + files.reduce((n, f) => n + f.size, 0);
	card.dataset.summary = `${dueFiles} files (${Format.formatBytes(dueSize)})`;

	if (groups.length === 0 && files.length === 0) {
		body.innerHTML = `<label class="af-section-label">Pending deletions</label><div class="af-text-muted">Nothing pending deletion.</div>`;
		return;
	}

	body.innerHTML = `
		<label class="af-section-label">Pending deletions</label>
		<div class="af-jr-summary">
			<span><strong>${dueFiles}</strong> files, <strong>${Format.formatBytes(dueSize)}</strong> due now
			(${due.length} groups, ${files.length} ungrouped)</span>
			${nextUpload.length ? `<span>· <strong>${nextUpload.length}</strong> older groups go on the next upload or change in their stream</span>` : ""}
		</div>
		<div class="af-jr-toolbar">
			<input type="search" class="af-input af-jr-filter" placeholder="Filter by stream, group or path">
			<button class="btn btn-ghost btn-sm af-jr-keep" disabled title="${esc(LOCK_NOTE)}">Keep selected</button>
		</div>
		<div class="af-jr-list"></div>
		<p class="af-jr-hint af-text-muted">Keep locks the files (immutable). Unlock them later in the
		file browser and their normal expiry applies again.</p>
	`;

	const list = body.querySelector(".af-jr-list");
	const keepBtn = body.querySelector(".af-jr-keep");
	const filter = body.querySelector(".af-jr-filter");

	const fileRow = (f, groupId) => `
		<label class="af-jr-file" data-search="${esc(f.path.toLowerCase())}">
			<input type="checkbox" class="af-jr-cb-file" data-id="${f.id}" ${groupId ? `data-group="${groupId}"` : ""}>
			<code title="${esc(f.path)}">${esc(f.path)}</code>
			${f.deadline ? `<span class="af-jr-size" title="Own expiry of this file">expired ${Format.dateTime(f.deadline)}</span>` : ""}
			<span class="af-jr-size">${Format.formatBytes(f.size)}</span>
		</label>`;

	const groupRow = (g) => {
		const r = REASON_LABEL[g.reason] || {
			text: g.reason,
			cls: "badge-outline",
		};
		const search =
			`${g.stream} ${g.group} ${g.files.map((f) => f.path).join(" ")}`.toLowerCase();
		return `
		<details class="af-jr-group" data-search="${esc(search)}">
			<summary>
				<input type="checkbox" class="af-jr-cb-group" data-id="${g.id}" title="${esc(`Keep the whole group. ${LOCK_NOTE}`)}">
				<span class="badge ${r.cls}">${r.text}</span>
				<span class="af-jr-name"><span class="af-text-muted">${esc(g.stream)}</span> / <strong>${esc(g.group)}</strong></span>
				<span class="af-jr-size">${g.files.length} files · ${Format.formatBytes(g.size)}</span>
			</summary>
			<div class="af-jr-files">
				${g.reason === "next_upload" ? `<div class="af-text-muted af-jr-deadline">Deleted the next time any file in stream ${esc(g.stream)} is uploaded or changed (including a download that renews an expire-after-download policy).</div>` : ""}
				${g.files.map((f) => fileRow(f, g.id)).join("")}
			</div>
		</details>`;
	};

	list.innerHTML =
		groups.map(groupRow).join("") +
		(files.length
			? `<details class="af-jr-group" open data-search="${esc(
					files
						.map((f) => f.path)
						.join(" ")
						.toLowerCase(),
				)}">
				<summary><span class="badge badge-danger">Due now</span>
				<span class="af-jr-name"><strong>Files without a group</strong></span>
				<span class="af-jr-size">${files.length} files</span></summary>
				<div class="af-jr-files">${files.map((f) => fileRow(f, null)).join("")}</div>
			</details>`
			: "");

	// Clicking the group checkbox must not toggle the <details>.
	list.querySelectorAll(".af-jr-cb-group").forEach((cb) => {
		cb.addEventListener("click", (e) => e.stopPropagation());
		cb.addEventListener("change", () => {
			list
				.querySelectorAll(`.af-jr-cb-file[data-group="${cb.dataset.id}"]`)
				.forEach((f) => {
					f.checked = cb.checked;
					f.disabled = cb.checked;
				});
			updateKeep();
		});
	});
	list
		.querySelectorAll(".af-jr-cb-file")
		.forEach((cb) => cb.addEventListener("change", updateKeep));

	// A ticked group counts as a group for the label, but what is sent is the
	// files listed here: the server's group_ids would also lock files uploaded
	// into the group after this page was rendered, which the admin never saw.
	function selection() {
		const groups = [...list.querySelectorAll(".af-jr-cb-group:checked")].length;
		const files = [
			...list.querySelectorAll(".af-jr-cb-file:checked:not(:disabled)"),
		].length;
		const resourceIds = [
			...list.querySelectorAll(".af-jr-cb-file:checked"),
		].map((cb) => Number(cb.dataset.id));
		return { groups, files, resourceIds };
	}

	function updateKeep() {
		const { groups, files } = selection();
		const n = groups + files;
		keepBtn.disabled = n === 0;
		keepBtn.textContent = n
			? `Keep selected (${groups ? `${groups} groups` : ""}${groups && files ? ", " : ""}${files ? `${files} files` : ""})`
			: "Keep selected";
	}

	keepBtn.onclick = async () => {
		const { resourceIds } = selection();
		keepBtn.disabled = true;
		try {
			const res = await API.keepPending([], resourceIds);
			showToast(`Kept ${res.locked} files (locked)`, "success");
			await renderPending(card, body);
		} catch (e) {
			showToast(e.message, "error");
			keepBtn.disabled = false;
		}
	};

	filter.addEventListener("input", () => {
		const q = filter.value.trim().toLowerCase();
		list.querySelectorAll(".af-jr-group").forEach((d) => {
			d.hidden = q !== "" && !d.dataset.search.includes(q);
		});
	});
}
