import { API } from "./api/ApiClient.js";
import { Auth } from "./api/Auth.js";
import { Format } from "./api/Format.js";
import { Permissions } from "./api/Permissions.js";
import { TransferManager } from "./api/TransferManager.js";
import { FileBrowser } from "./components/FileBrowser.js";
import { openLogin } from "./components/Login.js";
import { NotFoundView } from "./components/NotFoundView.js";
import { initGlobalSearch } from "./components/SearchAutocomplete.js";
import { openSettings } from "./components/Settings.js";
import { StreamManager } from "./components/StreamManager.js";
import { checkPendingToasts, showToast } from "./components/Toast.js";

const app = document.getElementById("app");

function renderUserMenu() {
	const container = document.querySelector(".af-navbar-right");
	const user = Auth.getUser();

	if (!Auth.isLoggedIn()) {
		container.innerHTML = `
            <div class="af-status-indicator af-status-guest" id="nav-login-btn">
                Guest
            </div>
        `;
		container.querySelector("#nav-login-btn").onclick = openLogin;
		return;
	}

	// --- AUTHENTICATED STATE ---
	const adminClass = user.isAdmin ? "af-status-admin" : "";

	container.innerHTML = `
		<div style="position: relative; margin-right: 8px; display: none;" id="nav-notifications-wrapper">
			<button class="btn btn-ghost" id="nav-notifications-btn" title="Alerts" style="position: relative; padding: 4px 8px;">
				🔔
				<span class="af-badge-notification hidden" id="nav-notifications-badge">0</span>
			</button>
			<div class="af-notification-dropdown hidden" id="nav-notifications-dropdown">
				<div class="af-notification-header">System Alerts</div>
				<div id="nav-notifications-list"></div>
			</div>
		</div>
        <div class="af-user-menu-container">
            <div class="af-status-indicator af-status-user ${adminClass}" id="user-menu-trigger">
                <span>${user.username}</span>
                ${user.isAdmin ? '<span class="af-role-label">(admin)</span>' : ""}
            </div>
            <div class="af-dropdown hidden" id="user-dropdown">
                <button class="af-dropdown-item" id="nav-settings-btn">⚙️ Settings</button>
                <div class="af-dropdown-divider"></div>
                <button class="af-dropdown-item" style="color: var(--danger)" id="nav-logout-btn">🚪 Logout</button>
            </div>
        </div>
    `;

	const trigger = container.querySelector("#user-menu-trigger");
	const dropdown = container.querySelector("#user-dropdown");

	trigger.onclick = (e) => {
		e.stopPropagation();
		dropdown.classList.toggle("hidden");
	};

	container.querySelector("#nav-settings-btn").onclick = (e) => {
		dropdown.classList.add("hidden");
		openSettings(e);
	};
	container.querySelector("#nav-logout-btn").onclick = () => {
		dropdown.classList.add("hidden");
		Auth.logout();
	};

	// Close dropdown when clicking outside
	document.addEventListener("click", (e) => {
		if (!dropdown.contains(e.target) && !trigger.contains(e.target)) {
			dropdown.classList.add("hidden");
		}
	});

	initNotifications();
}

async function initNotifications() {
	const user = Auth.getUser();
	const wrapper = document.getElementById("nav-notifications-wrapper");
	const btn = document.getElementById("nav-notifications-btn");
	const dropdown = document.getElementById("nav-notifications-dropdown");
	if (!wrapper || !btn || !dropdown) return;

	if (!user?.isAdmin) {
		wrapper.style.display = "none";
		return;
	}

	wrapper.style.display = "";

	// Re-attach listeners
	btn.onclick = (e) => {
		e.stopPropagation();
		dropdown.classList.toggle("hidden");
	};

	try {
		const failures = await API.getJanitorErrors();
		const badge = document.getElementById("nav-notifications-badge");
		const dropdownList = document.getElementById("nav-notifications-list");

		renderNotifications(failures, badge, dropdownList, dropdown);
	} catch (e) {
		console.error("Failed to fetch notifications", e);
	}
}

// A janitor failure's `last_error` already describes what went wrong; `type`
// tells us whether it was a file or group. Build a readable one-liner from both.
function notificationDetail(f) {
	const subject = f.type === "group" ? "Group cleanup" : "File cleanup";
	const reason = f.last_error ? f.last_error : "unknown error";
	const attempts = f.attempts > 1 ? ` (failed ${f.attempts}×)` : "";
	return `${subject} failed${attempts}: ${reason}`;
}

function renderNotifications(failures, badge, dropdownList, dropdown) {
	if (!failures || failures.length === 0) {
		badge.classList.add("hidden");
		dropdownList.innerHTML = `<div class="af-notification-item"><div class="af-notification-desc" style="text-align:center">No new alerts</div></div>`;
		return;
	}

	badge.classList.remove("hidden");
	badge.textContent = failures.length > 9 ? "9+" : failures.length;

	dropdownList.replaceChildren(
		...failures.map((f) => {
			const item = document.createElement("div");
			item.className = "af-notification-item";
			item.innerHTML = `
				<button type="button" class="af-notification-dismiss" title="Dismiss">&times;</button>
				<div class="af-notification-title">Cleanup Failure</div>
				<div class="af-notification-path">${Format.escapeHtml(f.key)}</div>
				<div class="af-notification-desc">${Format.escapeHtml(notificationDetail(f))}</div>
			`;

			// Click the body -> open Settings and jump to the Janitor card.
			item.addEventListener("click", () => {
				dropdown.classList.add("hidden");
				openSettings();
				goToJanitorCard();
			});

			// Dismiss (X) -> clear the failure on the backend, then refresh.
			const dismissBtn = item.querySelector(".af-notification-dismiss");
			dismissBtn.addEventListener("click", async (e) => {
				e.stopPropagation();
				dismissBtn.disabled = true;
				try {
					await API.clearJanitorError(f.key);
					await initNotifications();
					window.dispatchEvent(new CustomEvent("af:settings-refresh"));
				} catch (err) {
					showToast(err.message, "error");
					dismissBtn.disabled = false;
				}
			});

			return item;
		}),
	);
}

// After opening Settings, the System tab (with the Janitor card) renders
// asynchronously. Poll briefly for the card, then scroll to and flash it.
function goToJanitorCard() {
	let tries = 0;
	const timer = setInterval(() => {
		const card = document.getElementById("janitor-failures-card");
		if (card && card.style.display !== "none") {
			clearInterval(timer);
			card.scrollIntoView({ behavior: "smooth", block: "center" });
			card.classList.add("af-flash-highlight");
			setTimeout(() => card.classList.remove("af-flash-highlight"), 1600);
		} else if (++tries > 40) {
			clearInterval(timer);
		}
	}, 50);
}

function updateActiveNavLink(activeRoute) {
	const navLinks = document.querySelectorAll(".af-navbar-link[data-route]");
	navLinks.forEach((link) => {
		// 3. Toggle the 'active' class based on the match
		if (link.dataset.route === activeRoute) {
			link.classList.add("active");
		} else {
			link.classList.remove("active");
		}
	});
}

/**
 * Router Logic: Decides which component to render based on the URL
 */
async function router() {
	// Cleanup global event listeners when switching views
	if (window._afDragOverHandler) {
		document.body.removeEventListener("dragover", window._afDragOverHandler);
		document.body.removeEventListener("dragenter", window._afDragOverHandler);
		document.body.removeEventListener("dragleave", window._afDragLeaveHandler);
		document.body.removeEventListener("drop", window._afDropHandler);
		window._afDragOverHandler = null;
		window._afDragLeaveHandler = null;
		window._afDropHandler = null;
	}

	const path = window.location.pathname;

	renderUserMenu();
	checkPendingToasts();

	if (path.startsWith("/_/streams")) {
		updateActiveNavLink("streams");
		const view = await StreamManager(path);
		renderView(view);
		return;
	}

	// 2. Default: File Browser
	const wasLoggedIn = Auth.isLoggedIn();
	try {
		updateActiveNavLink("files");
		const view = await FileBrowser(path);
		renderView(view);
	} catch (err) {
		console.error(err);

		// --- 401 RETRY LOGIC ---
		if (err.status === 401) {
			// sessionExpired=true: this request cleared the token
			// wasLoggedIn && !Auth.isLoggedIn(): a concurrent request (e.g. initNotifications)
			//   already cleared the token before listFiles got its 401 response
			if (err.sessionExpired || (wasLoggedIn && !Auth.isLoggedIn())) {
				console.log("Session just expired, retrying as guest...");
				return router();
			}
			app.innerHTML = `<div class="af-alert af-alert-error">Authentication Required: Please sign in to view this path.</div>`;
			return;
		}

		// --- 403 FORBIDDEN ---
		// err.message is the server's error string, which embeds the requested
		// path ("Action prohibited: /<path> is immutable"). The path is chosen by
		// whoever uploaded it, so it is escaped rather than parsed as markup.
		if (err.status === 403) {
			app.innerHTML = `
                <div class="af-card" style="text-align:center; padding:40px;">
                    <div style="font-size: 40px">🚫</div>
                    <h2>Access Denied</h2>
                    <p class="af-text-muted">${Format.escapeHtml(err.message)}</p>
                    <a href="/" class="btn btn-primary nav-link" style="margin-top:20px">Go to Home</a>
                </div>
            `;
			return;
		}

		if (err.status === 404) {
			renderView(NotFoundView(path));
		} else {
			// General error (e.g. 500) — same reasoning as the 403 branch above.
			app.innerHTML = `<div class="af-alert af-alert-error"><strong>Error:</strong> ${Format.escapeHtml(err.message)}</div>`;
		}
	}

	applyUIPermissions();
}

/**
 * Helper to safely swap views
 * @param {Node} componentNode - The DocumentFragment or Element from the component
 */
function renderView(componentNode) {
	// replaceChildren is the modern, performant way to clear and set content
	app.replaceChildren(componentNode);
}

/**
 * Navigation Handler
 */
window.addEventListener("popstate", () => {
	router();
});
window.addEventListener("pageshow", (event) => {
	// If event.persisted is true, the page was restored from bfcache
	if (event.persisted) {
		console.log("Restored from bfcache, forcing router update...");
		router();
	}
});

document.body.addEventListener("click", (e) => {
	// 1. Find the nearest parent (or self) that has the .nav-link class
	const link = e.target.closest(".nav-link");

	// 2. If we didn't click a .nav-link, or it's a right-click/ctrl-click, ignore
	if (!link) return;

	// 3. (Pro Tip) Standard SPA behavior: Allow middle-clicks or Ctrl/Cmd clicks
	// to open in a new tab normally.
	if (e.button !== 0 || e.ctrlKey || e.metaKey || e.shiftKey || e.altKey)
		return;

	console.log("SPA Navigation to:", link.getAttribute("href"));

	e.preventDefault();
	const url = link.getAttribute("href");

	window.history.pushState({}, "", url);
	router();
});

window.addEventListener("af:session-expired", () => {
	showToast(
		"🕒 Your session has expired. You are now browsing as a guest.",
		"warning",
		{ label: "Login Again", callback: openLogin },
	);

	// Update the Navbar user pill to 'Guest' dot
	if (typeof renderUserMenu === "function") renderUserMenu();
});

window.addEventListener("artifactory:navigated", () => {
	router();
});

// Listen for the custom event we dispatched in TransferManager
window.addEventListener("artifactory:refresh", (e) => {
	const updatedPath = e.detail.path;
	const currentPath = window.location.pathname;

	// Re-check UI permissions whenever data refreshes
	applyUIPermissions();

	// Only refresh if the user is currently looking at the folder where the file was uploaded
	// or if they are at the root.
	console.log("refresh event", currentPath, e);
	if (currentPath === updatedPath || currentPath === "/") {
		router(); // Re-run the router to fetch new data and re-render
	}
});

/**
 * Handle Auth Failures globally
 */
window.addEventListener("af:require-login", () => {
	console.warn("Authentication required - opening login dialog");

	// 1. Clear any stale user data
	localStorage.removeItem("af_token");
	localStorage.removeItem("af_user");

	// 2. Open the login dialog
	openLogin();
});

function applyUIPermissions() {
	const isLoggedIn = !!localStorage.getItem("af_token");
	const writeElements = document.querySelectorAll(".af-requires-auth");

	writeElements.forEach((el) => {
		el.style.display = isLoggedIn ? "" : "none";
	});

	Permissions.applyGlobalRules(window.location.pathname);
}

// Initialize the history/UI
TransferManager.init();
initGlobalSearch();

// Click outside handler for dropdowns
document.addEventListener("click", (e) => {
	const notifDropdown = document.getElementById("nav-notifications-dropdown");
	const notifBtn = document.getElementById("nav-notifications-btn");
	if (
		notifDropdown &&
		!notifDropdown.contains(e.target) &&
		notifBtn &&
		!notifBtn.contains(e.target)
	) {
		notifDropdown.classList.add("hidden");
	}
});

// Initial Load
router();
