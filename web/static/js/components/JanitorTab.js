import { API } from "../api/ApiClient.js";
import { Format } from "../api/Format.js";
import { JanitorReview } from "./JanitorReview.js";
import { showToast } from "./Toast.js";

// Settings → Janitor (admin only): status and controls with the pending
// deletions, cleanup failures, and a log of what the janitor did.

const esc = (s) => Format.escapeHtml(String(s ?? ""));

export async function JanitorTab() {
	const root = document.createElement("div");
	root.className = "af-janitor-tab";

	let status;
	try {
		status = await API.getJanitorStatus();
	} catch (e) {
		root.innerHTML = `<div class="af-text-muted">Janitor status unavailable: ${esc(e.message)}</div>`;
		return root;
	}

	const [review, failures] = await Promise.all([
		JanitorReview(status),
		FailuresCard(),
	]);
	root.append(review, failures, LogCard());
	return root;
}

async function FailuresCard() {
	const card = document.createElement("div");
	card.className = "af-card";
	card.id = "janitor-failures-card";

	let failures = [];
	try {
		failures = (await API.getJanitorErrors()) || [];
	} catch (e) {
		card.innerHTML = `<label class="af-section-label" style="margin-top:0">Cleanup failures</label>
			<div class="af-text-muted">Failed to load: ${esc(e.message)}</div>`;
		return card;
	}

	if (failures.length === 0) {
		card.innerHTML = `<label class="af-section-label" style="margin-top:0">Cleanup failures</label>
			<div class="af-text-muted">None. Files the janitor fails to delete are listed here and retried with backoff.</div>`;
		return card;
	}

	card.innerHTML = `
		<label class="af-section-label" style="color: var(--danger); margin-top: 0;">Cleanup failures</label>
		<div class="af-table-wrapper">
			<table class="af-table af-table-compact af-janitor-table">
				<thead><tr><th>Type</th><th>Identifier</th><th>Attempts</th><th>Last Error</th><th>Next Retry</th><th>Actions</th></tr></thead>
				<tbody>${failures
					.map(
						(f) => `
					<tr>
						<td><span class="badge ${f.type === "group" ? "badge-info" : "badge-outline"}">${esc(f.type)}</span></td>
						<td class="af-janitor-key"><code title="${esc(f.key)}">${esc(f.key)}</code></td>
						<td style="text-align:center">${f.attempts}</td>
						<td class="af-janitor-error" title="${esc(f.last_error)}">${esc(f.last_error)}</td>
						<td class="af-col-mono" style="font-size:11px;white-space:nowrap">${Format.dateTime(f.retry_after)}</td>
						<td><button class="btn btn-ghost btn-sm btn-clear-janitor" data-key="${esc(f.key)}">Clear</button></td>
					</tr>`,
					)
					.join("")}</tbody>
			</table>
		</div>`;

	card.querySelectorAll(".btn-clear-janitor").forEach((btn) => {
		btn.onclick = async () => {
			btn.disabled = true;
			btn.innerText = "Clearing...";
			try {
				const res = await API.clearJanitorError(btn.dataset.key);
				showToast(res.message, "success");
				window.dispatchEvent(new CustomEvent("af:settings-refresh"));
				window.dispatchEvent(new CustomEvent("af:notifications-refresh"));
			} catch (err) {
				showToast(err.message, "error");
				btn.disabled = false;
				btn.innerText = "Clear";
			}
		};
	});
	return card;
}

// Janitor log: the audit entries the janitor and its controls write.
// SYSTEM_CLEANUP = a file the janitor deleted (or skipped); "janitor_" =
// suspend / resume / keep actions by admins.
const LOG_SOURCES = ["SYSTEM_CLEANUP", "janitor_"];

function describe(e) {
	if (e.action === "SYSTEM_CLEANUP") {
		const meta = e.meta && typeof e.meta === "object" ? e.meta : {};
		const where =
			meta.stream && meta.group ? ` · ${meta.stream} / ${meta.group}` : "";
		if (e.status !== "SUCCESS") {
			return {
				event: "Kept",
				cls: "badge-warning",
				detail: `${e.error || e.reason || "skipped"}${where}`,
			};
		}
		return {
			event: "Deleted",
			cls: "badge-danger",
			detail: `${e.reason || ""}${where}`,
		};
	}
	switch (e.operation) {
		case "janitor_suspend":
			return {
				event: "Suspended",
				cls: "badge-warning",
				detail: e.reason || "",
			};
		case "janitor_resume":
			return {
				event: "Resumed",
				cls: "badge-success",
				detail: e.was_suspended_for ? `was: ${e.was_suspended_for}` : "",
			};
		case "janitor_review_keep":
			return { event: "Locked (keep)", cls: "badge-info", detail: "" };
		default:
			return {
				event: e.operation || e.action,
				cls: "badge-outline",
				detail: "",
			};
	}
}

function LogCard() {
	const card = document.createElement("div");
	card.className = "af-card af-janitor-log";
	card.innerHTML = `
		<label class="af-section-label" style="margin-top:0">Janitor log</label>
		<div class="af-jr-toolbar">
			<input type="search" class="af-jr-filter af-jl-filter" placeholder="Filter by path, stream, reason or user">
		</div>
		<div class="af-table-wrapper af-jl-wrapper">
			<table class="af-table af-table-compact af-jl-table">
				<thead><tr><th>Time</th><th>Event</th><th>Path</th><th>Details</th><th>By</th></tr></thead>
				<tbody></tbody>
			</table>
		</div>
		<div class="af-jl-footer">
			<span class="af-text-muted af-jl-note"></span>
			<button type="button" class="btn btn-ghost btn-sm af-jl-more" hidden>Load more</button>
		</div>`;

	const tbody = card.querySelector("tbody");
	const more = card.querySelector(".af-jl-more");
	const note = card.querySelector(".af-jl-note");
	const filter = card.querySelector(".af-jl-filter");

	let beforeOffset = -1;
	let generation = 0;
	let loading = false;
	// Each request gets a number; only the latest one may update the table.
	// A reset (new filter) supersedes whatever is in flight instead of being
	// dropped, so the table always matches the filter box.
	let seq = 0;

	async function load(reset) {
		if (loading && !reset) return; // "Load more" while busy: ignore
		const mine = ++seq;
		loading = true;
		if (reset) {
			beforeOffset = -1;
			generation = 0;
			tbody.innerHTML = `<tr><td colspan="5" class="af-text-muted">Loading…</td></tr>`;
		}
		more.disabled = true;
		try {
			const page = await API.getAuditLog({
				beforeOffset,
				generation,
				limit: 100,
				filter: filter.value.trim(),
				any: LOG_SOURCES,
			});
			if (mine !== seq) return; // superseded by a newer request
			if (reset) tbody.innerHTML = "";
			tbody.insertAdjacentHTML(
				"beforeend",
				page.entries
					.map((e) => {
						const d = describe(e);
						const isPath = e.action === "SYSTEM_CLEANUP";
						return `<tr>
							<td class="af-col-mono" style="font-size:11px;white-space:nowrap">${Format.dateTime(e.time)}</td>
							<td><span class="badge ${d.cls}">${esc(d.event)}</span></td>
							<td class="af-jl-path">${isPath ? `<code title="${esc(e.resource)}">${esc(e.resource)}</code>` : ""}</td>
							<td class="af-jl-detail" title="${esc(d.detail)}">${esc(d.detail)}</td>
							<td>${esc(e.user || (isPath ? "system" : ""))}</td>
						</tr>`;
					})
					.join(""),
			);
			if (!tbody.children.length) {
				tbody.innerHTML = `<tr><td colspan="5" class="af-text-muted">No janitor activity${filter.value.trim() ? " matching the filter" : ""}.</td></tr>`;
			}
			beforeOffset = page.next_before_offset;
			generation = page.next_generation ?? 0;
			more.hidden = !page.has_more;
			note.textContent = page.scan_limit_hit
				? "Stopped scanning a very large log; load more to continue."
				: "";
		} catch (e) {
			if (mine !== seq) return;
			if (reset) tbody.innerHTML = "";
			note.textContent = `Failed to load: ${e.message}`;
		} finally {
			if (mine === seq) {
				loading = false;
				more.disabled = false;
			}
		}
	}

	more.onclick = () => load(false);
	let timer;
	filter.addEventListener("input", () => {
		clearTimeout(timer);
		timer = setTimeout(() => load(true), 300);
	});

	load(true);
	return card;
}
