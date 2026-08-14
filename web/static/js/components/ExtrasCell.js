import { Format } from "../api/Format.js";

function getExtrasPopover() {
	let popover = document.getElementById("af-extras-popover");
	if (!popover) {
		popover = document.createElement("div");
		popover.id = "af-extras-popover";
		popover.className = "af-extras-popover hidden";
		document.body.appendChild(popover);
	}
	return popover;
}

function showExtrasPopover(extrasInner) {
	const stream = extrasInner.dataset.stream;
	const group = extrasInner.dataset.group;
	const tagsRaw = extrasInner.dataset.tags;
	const expiresAt = extrasInner.dataset.expiresAt;

	if (!stream && !tagsRaw && !expiresAt) return;

	let html = "";
	if (stream) {
		html += `<div class="af-extras-popover-section">
			<div class="af-extras-popover-label">Stream / Group</div>
			<div class="af-extras-popover-value">${stream}/${group}</div>
		</div>`;
	}
	if (tagsRaw) {
		const tags = JSON.parse(tagsRaw);
		const tagStr = tags
			.map((t) => (t.value ? `${t.key}=${t.value}` : t.key))
			.join(", ");
		html += `<div class="af-extras-popover-section">
			<div class="af-extras-popover-label">Tags</div>
			<div class="af-extras-popover-value">${tagStr}</div>
		</div>`;
	}
	if (expiresAt) {
		html += `<div class="af-extras-popover-section">
			<div class="af-extras-popover-label">Expires</div>
			<div class="af-extras-popover-value">${Format.dateTime(expiresAt)}</div>
		</div>`;
	}

	const popover = getExtrasPopover();
	popover.innerHTML = html;

	const rect = extrasInner.getBoundingClientRect();
	popover.style.top = `${rect.bottom + 4}px`;
	popover.style.left = `${Math.min(rect.left, window.innerWidth - 280)}px`;
	popover.classList.remove("hidden");

	setTimeout(() => {
		document.addEventListener("click", () => popover.classList.add("hidden"), {
			once: true,
		});
	}, 0);
}

/**
 * Populates an .af-extras-inner element with stream/tags/expiry badges for a file.
 * opts.showStream — set false to suppress the stream badge (e.g. stream view where
 *                   stream/group is already shown in the group header row).
 */
export function renderExtrasCell(file, extrasInner, opts = {}) {
	const showStream = opts.showStream !== false;

	if (showStream && file.stream) {
		extrasInner.dataset.stream = file.stream;
		extrasInner.dataset.group = file.group || "";
		const badge = document.createElement("span");
		badge.className = "badge-origin badge-stream af-extras-badge";
		badge.textContent = `${file.stream}/${file.group}`;
		badge.title = `${file.stream}/${file.group}`;
		extrasInner.appendChild(badge);
	}

	if (file.tags && Array.isArray(file.tags) && file.tags.length > 0) {
		extrasInner.dataset.tags = JSON.stringify(file.tags);
		file.tags.forEach((tag, i) => {
			const v = tag.value ? `=${tag.value}` : "";
			const span = document.createElement("span");
			span.className = `badge-tag af-extras-badge${i >= 1 ? " af-tag-overflow" : ""}`;
			span.textContent = `${tag.key}${v}`;
			extrasInner.appendChild(span);
		});
		if (file.tags.length > 1) {
			const more = document.createElement("span");
			more.className = "af-extras-more";
			more.textContent = `+${file.tags.length - 1}`;
			extrasInner.appendChild(more);
		}
	}

	const computedAt = file.retention?.expires?.effective;
	if (computedAt && !computedAt.startsWith("0001")) {
		extrasInner.dataset.expiresAt = computedAt;
		const expiryEl = document.createElement("span");
		expiryEl.className = "expiry-info af-extras-badge";
		expiryEl.textContent = `⏳ ${Format.timeRemaining(computedAt)}`;
		expiryEl.title = `Expires at ${Format.dateTime(computedAt)}`;
		if (Format.isExpired(computedAt)) {
			expiryEl.classList.add("expiry-critical");
		} else if (Format.isNearExpiry(computedAt, 24)) {
			expiryEl.classList.add("expiry-warning");
		}
		extrasInner.appendChild(expiryEl);
	}

	if (extrasInner.children.length > 0) {
		extrasInner.onclick = (e) => {
			if (extrasInner.closest(".af-table-selection-mode")) return;
			e.stopPropagation();
			showExtrasPopover(extrasInner);
		};
	}
}

/**
 * Wires up the eye toggle button for a table.
 * Persists state in localStorage so both FileBrowser and StreamManager
 * share the same expanded/collapsed preference across page reloads.
 */
export function initExtrasEye(tableEl, eyeBtn) {
	const expanded = localStorage.getItem("af_extras_expanded") === "true";
	if (expanded) {
		tableEl.classList.add("af-extras-expanded");
		eyeBtn.classList.add("active");
	}
	eyeBtn.onclick = (e) => {
		e.stopPropagation();
		const isExpanded = tableEl.classList.toggle("af-extras-expanded");
		eyeBtn.classList.toggle("active", isExpanded);
		localStorage.setItem("af_extras_expanded", isExpanded);
		getExtrasPopover().classList.add("hidden");
	};
}
