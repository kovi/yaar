import { openAuditLog } from "./AuditLog.js";
import { JanitorTab } from "./JanitorTab.js";
import { SystemInfo } from "./SystemInfo.js";
import { TokenManagement } from "./TokenManagement.js";
import { UserManagement } from "./UserManagement.js";

const template = document.createElement("template");
template.innerHTML = `
    <dialog class="af-modal af-modal-settings" id="settings-dialog">
        <div class="af-modal-header">
            <h3>Settings</h3>
            <div class="af-modal-header-actions">
                <button type="button" class="btn btn-ghost af-modal-maximize" title="Maximize">⤢</button>
                <button type="button" class="btn btn-ghost modal-close">×</button>
            </div>
        </div>

        <nav class="af-tabs">
            <button class="af-tab-btn active" data-tab="system">System</button>
            <button class="af-tab-btn af-admin-only" data-tab="janitor">Janitor</button>
            <button class="af-tab-btn af-admin-only" data-tab="users">Users</button>
            <button class="af-tab-btn" data-tab="tokens">Tokens</button>
            <button class="af-tab-btn af-admin-only" data-tab="audit">Audit Log</button>
        </nav>

        <div class="af-modal-body">
            <!-- System Tab -->
            <div class="af-tab-content active" id="tab-system"></div>

            <!-- Janitor Tab -->
            <div class="af-tab-content" id="tab-janitor"></div>

            <!-- Users Tab -->
            <div class="af-tab-content" id="tab-users"></div>

            <!-- Tokens Tab -->
            <div class="af-tab-content" id="tab-tokens"></div>
        </div>

        <div class="af-modal-footer">
            <button type="button" class="btn btn-primary modal-close">Close</button>
        </div>
    </dialog>
`;

export async function openSettings(tab = "system") {
	if (!document.getElementById("settings-dialog")) {
		document.body.appendChild(template.content.cloneNode(true));

		const dialog = document.getElementById("settings-dialog");
		dialog.querySelectorAll(".modal-close").forEach((btn) => {
			btn.onclick = () => dialog.close();
		});
		initDialogSizing(dialog);
	}

	const dialog = document.getElementById("settings-dialog");
	const user = JSON.parse(localStorage.getItem("af_user"));

	// 1. Hide Admin Tabs if user is not admin
	dialog.querySelectorAll(".af-admin-only").forEach((el) => {
		el.style.display = user.isAdmin ? "block" : "none";
	});

	// 2. Tab Switching Logic
	dialog.querySelectorAll(".af-tab-btn").forEach((btn) => {
		btn.onclick = async () => {
			if (btn.dataset.tab === "audit") {
				openAuditLog();
				return;
			}

			dialog
				.querySelectorAll(".af-tab-btn, .af-tab-content")
				.forEach((el) => el.classList.remove("active"));
			btn.classList.add("active");
			const target = dialog.querySelector(`#tab-${btn.dataset.tab}`);
			target.classList.add("active");

			if (btn.dataset.tab === "users") {
				target.replaceChildren(await UserManagement());
			} else if (btn.dataset.tab === "janitor") {
				target.replaceChildren(await JanitorTab());
			} else if (btn.dataset.tab === "system") {
				target.replaceChildren(await SystemInfo());
			} else if (btn.dataset.tab === "tokens") {
				target.replaceChildren(await TokenManagement());
			}
		};
	});

	// Initial activation of the requested tab (falls back to the first one,
	// e.g. a non-admin asked for an admin tab).
	const initial = dialog.querySelector(`.af-tab-btn[data-tab="${tab}"]`);
	(initial && initial.style.display !== "none"
		? initial
		: dialog.querySelector(".af-tab-btn")
	).click();
	dialog.showModal();
}

// Helper to allow child components to refresh the view
window.addEventListener("af:settings-refresh", async () => {
	const activeTab = document.querySelector(".af-tab-btn.active");
	if (!activeTab) {
		return;
	}

	const target = document.querySelector(`#tab-${activeTab.dataset.tab}`);
	if (activeTab.dataset.tab === "users") {
		target.replaceChildren(await UserManagement());
	} else if (activeTab.dataset.tab === "system") {
		target.replaceChildren(await SystemInfo());
	} else if (activeTab.dataset.tab === "janitor") {
		target.replaceChildren(await JanitorTab());
	} else if (activeTab.dataset.tab === "tokens") {
		target.replaceChildren(await TokenManagement());
	}
});

// Settings holds wide content (janitor review, audit, config), so the dialog
// can be resized by its corner or maximized. Both are remembered per browser.
const SIZE_KEY = "af_settings_dialog_size";

function loadSize() {
	try {
		return JSON.parse(localStorage.getItem(SIZE_KEY)) || {};
	} catch {
		return {};
	}
}

function saveSize(patch) {
	try {
		localStorage.setItem(SIZE_KEY, JSON.stringify({ ...loadSize(), ...patch }));
	} catch {
		// storage unavailable: sizing just isn't remembered
	}
}

function initDialogSizing(dialog) {
	const btn = dialog.querySelector(".af-modal-maximize");
	const saved = loadSize();

	if (saved.width && saved.height) {
		dialog.style.width = saved.width;
		dialog.style.height = saved.height;
	}

	const setMaximized = (on) => {
		dialog.classList.toggle("af-modal-maximized", on);
		btn.textContent = on ? "⤡" : "⤢";
		btn.title = on ? "Restore size" : "Maximize";
	};
	setMaximized(!!saved.maximized);

	btn.onclick = () => {
		const on = !dialog.classList.contains("af-modal-maximized");
		setMaximized(on);
		saveSize({ maximized: on });
	};

	// The browser writes the dragged size into inline width/height; remember
	// it. Content changes don't touch the inline style, so they are ignored.
	let timer;
	new ResizeObserver(() => {
		if (!dialog.open || dialog.classList.contains("af-modal-maximized")) return;
		if (!dialog.style.width || !dialog.style.height) return;
		clearTimeout(timer);
		timer = setTimeout(
			() =>
				saveSize({ width: dialog.style.width, height: dialog.style.height }),
			300,
		);
	}).observe(dialog);
}

// Lets components switch tabs, e.g. System → Operations → Janitor "Details".
window.addEventListener("af:settings-tab", (e) => {
	document
		.querySelector(`#settings-dialog .af-tab-btn[data-tab="${e.detail}"]`)
		?.click();
});
