/**
 * Shows a transient notification.
 *
 * `message` is always rendered as text, never as markup: most callers pass a
 * server error string, which echoes back paths and filenames chosen by whoever
 * uploaded them. Callers that need to list several items pass `details` (an
 * array of strings) instead of building HTML themselves.
 */
export function showToast(
	message,
	type = "info",
	action = null,
	duration = 8000,
	details = null,
) {
	let container = document.querySelector(".af-toast-container");
	if (!container) {
		container = document.createElement("div");
		container.className = "af-toast-container";
		// 'manual' means it won't close automatically when you click outside
		container.setAttribute("popover", "manual");
		document.body.appendChild(container);
	}

	// PROMOTE TO TOP LAYER:
	// If it's not already visible in the top layer, show it.
	try {
		if (!container.matches(":popover-open")) {
			container.showPopover();
		}
	} catch (_e) {
		// Fallback for older browsers: z-index 10000 (standard behavior)
		container.style.zIndex = "10000";
	}

	const toast = document.createElement("div");
	toast.className = `af-toast af-toast-${type}`;

	// Toast messages are frequently server error strings, which echo back paths
	// and filenames the caller does not control (e.g. "Action prohibited:
	// /<img src=x onerror=...>.txt is immutable"). Build the nodes and assign
	// through textContent so a message is always shown as text, never parsed as
	// markup.
	const content = document.createElement("div");
	content.className = "af-toast-content";
	content.textContent = message ?? "";

	// Optional per-item detail lines (e.g. which paths in a batch failed).
	// Built as nodes with textContent for the same reason as the message.
	if (Array.isArray(details) && details.length > 0) {
		const list = document.createElement("ul");
		list.style.margin = "6px 0 0";
		list.style.paddingLeft = "18px";
		for (const line of details) {
			const li = document.createElement("li");
			li.textContent = line;
			list.appendChild(li);
		}
		content.appendChild(list);
	}

	toast.appendChild(content);

	if (action) {
		const actionBtn = document.createElement("button");
		actionBtn.className = "btn btn-primary btn-sm toast-action";
		actionBtn.textContent = action.label ?? "";
		actionBtn.onclick = () => {
			action.callback();
			toast.remove();
		};
		toast.appendChild(actionBtn);
	}

	const closeBtn = document.createElement("button");
	closeBtn.className = "btn btn-ghost btn-sm";
	closeBtn.textContent = "×";
	closeBtn.onclick = () => toast.remove();
	toast.appendChild(closeBtn);

	container.appendChild(toast);

	if (duration > 0) {
		setTimeout(() => {
			toast.classList.add("fade-out");
			setTimeout(() => toast.remove(), 300);
		}, duration);
	}
}

export function checkPendingToasts() {
	const pendingJson = sessionStorage.getItem("af_pending_toast");
	if (pendingJson) {
		try {
			const { message, type } = JSON.parse(pendingJson);
			showToast(message, type);
		} catch (e) {
			console.error("Failed to parse pending toast", e);
		}
		// CRITICAL: Remove it so it doesn't show again if they refresh manually
		sessionStorage.removeItem("af_pending_toast");
	}
}
