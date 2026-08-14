import { API } from "../api/ApiClient.js";
import { Format } from "../api/Format.js";
import { Path } from "../api/Path.js";
import { initExtrasEye, renderExtrasCell } from "./ExtrasCell.js";
import { openFileInfo } from "./FileInfo.js";
import { isPreviewable, openPreviewDialog } from "./PreviewDialog.js";
import { showToast } from "./Toast.js";

const ICON_LOCATE = `
<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
    <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"></path>
    <circle cx="12" cy="13" r="3"></circle>
</svg>`;

const createStreamTemplate = document.createElement("template");
createStreamTemplate.innerHTML = `
    <dialog class="af-modal" id="create-stream-dialog" style="max-width: 450px;">
        <form class="af-form">
            <div class="af-modal-header">
                <h3>Create New Stream</h3>
                <button type="button" class="btn btn-ghost modal-close">×</button>
            </div>
            <div class="af-modal-body">
                <label style="display: flex; flex-direction: column; gap: 4px;">
                    <span>Stream Identifier</span>
                    <input type="text" name="stream_name" required placeholder="e.g. project-name" style="padding: 10px; border: 1px solid var(--border); border-radius: var(--radius); font-size: 14px;">
                </label>
                <div style="border: 1px solid var(--border); border-radius: var(--radius); padding: 12px; margin-top: 12px; background: var(--bg-alt); display: flex; flex-direction: column; gap: 10px;">
                    <label class="af-check-group" style="margin: 0;">
                        <input type="checkbox" name="retain_latest">
                        <span>Retain Latest Group</span>
                    </label>
                    <label style="margin: 0; display: flex; flex-direction: column; gap: 4px;">
                        <span>Latest Group Max Expiry</span>
                        <input type="text" name="retain_latest_max_expiry" placeholder="e.g. 90d" style="padding: 10px; border: 1px solid var(--border); border-radius: var(--radius); font-size: 14px;">
                    </label>
                    <label class="af-check-group" style="margin: 0;">
                        <input type="checkbox" name="auto_expire_previous">
                        <span>Auto Expire Previous Groups</span>
                    </label>
                </div>
            </div>
            <div class="af-modal-footer">
                <button type="button" class="btn btn-ghost modal-close">Cancel</button>
                <button type="submit" class="btn btn-primary">Create</button>
            </div>
        </form>
    </dialog>
`;

const streamSettingsTemplate = document.createElement("template");
streamSettingsTemplate.innerHTML = `
    <dialog class="af-modal" id="manager-stream-settings-dialog" style="max-width: 400px;">
        <form class="af-form">
            <div class="af-modal-header">
                <h3>Stream Settings</h3>
                <button type="button" class="btn btn-ghost modal-close">×</button>
            </div>
            <div class="af-modal-body">
                <div style="margin-bottom: 15px;">
                    <label class="af-check-group">
                        <input type="checkbox" id="detail-retain-latest"> 
                        <span>Retain Latest Group</span>
                    </label>
                    <small class="af-input-help" style="margin-left: 28px; display: block;">The latest group's expiry is paused/inactive, keeping it alive.</small>
                </div>
                <div style="margin-bottom: 15px;">
                    <label style="display: flex; flex-direction: column; gap: 4px;">
                        <span>Latest Group Max Expiry</span>
                        <input type="text" id="detail-max-expiry" placeholder="e.g. 90d" style="padding: 10px; border: 1px solid var(--border); border-radius: var(--radius); font-size: 14px;">
                    </label>
                    <small class="af-input-help" style="display: block; margin-top: 4px;">If "Retain Latest" is on, this forces the latest group to eventually expire.</small>
                </div>
                <div style="margin-bottom: 15px;">
                    <label class="af-check-group">
                        <input type="checkbox" id="detail-auto-expire-previous"> 
                        <span>Auto Expire Previous Groups</span>
                    </label>
                    <small class="af-input-help" style="margin-left: 28px; display: block;">When a new group is uploaded, all previous groups expire immediately.</small>
                </div>
            </div>
            <div class="af-modal-footer">
                <button type="button" class="btn btn-ghost modal-close">Cancel</button>
                <button type="submit" class="btn btn-primary" id="save-stream-settings-btn">Save Settings</button>
            </div>
        </form>
    </dialog>
`;

export async function StreamManager(path) {
	// Strip the "/_/streams" prefix; whatever remains is the (URL-encoded)
	// stream name, which may itself contain encoded slashes (%2F).
	const rest = path.replace(/^\/_\/streams\/?/, "");

	if (rest === "") {
		return renderStreamList();
	}
	const streamName = decodeURIComponent(rest);
	return renderStreamDetail(streamName);
}

async function renderStreamList() {
	const streams = await API.getStreams();
	const view = document.createElement("div");

	view.innerHTML = `
        <nav class="af-breadcrumb" style="display: flex; justify-content: space-between; align-items: center;">
            <div class="af-breadcrumb-item"><span class="af-breadcrumb-current">📡 Streams</span></div>
            <button class="btn btn-primary" id="create-stream-btn" style="font-size: 12px; padding: 4px 10px;">➕ Create Stream</button>
        </nav>
        <div class="af-table-wrapper">
            <table class="af-table af-table-clickable">
                <thead>
                    <tr>
                        <th>Stream Identifier</th>
                        <th style="text-align: right; color: var(--text-muted); font-weight: normal; font-size: 11px;">Click row to open</th>
                    </tr>
                </thead>
                <tbody id="stream-list-body"></tbody>
            </table>
        </div>
    `;

	const tbody = view.querySelector("#stream-list-body");

	if (streams.length === 0) {
		const row = document.createElement("tr");
		row.innerHTML = `<td colspan="2">No streams</td>`;
		tbody.appendChild(row);
	}
	streams.forEach((s) => {
		const row = document.createElement("tr");
		row.classList.add("af-file-row");
		row.innerHTML = `
            <td><strong class="af-link-text"><span class="badge badge-origin badge-stream">${Format.escapeHtml(s)}</span></strong></td>
            <td style="text-align: right; color: var(--border);">→</td>
        `;

		// Row-level navigation logic
		row.onclick = () => {
			window.history.pushState(null, "", `/_/streams/${encodeURIComponent(s)}`);
			// Trigger the SPA router
			window.dispatchEvent(new CustomEvent("artifactory:navigated"));
		};

		tbody.appendChild(row);
	});

	const createBtn = view.querySelector("#create-stream-btn");
	createBtn.onclick = () => {
		let dialog = document.getElementById("create-stream-dialog");
		if (!dialog) {
			document.body.appendChild(createStreamTemplate.content.cloneNode(true));
			dialog = document.getElementById("create-stream-dialog");

			dialog.querySelectorAll(".modal-close").forEach((btn) => {
				btn.onclick = () => dialog.close();
			});

			const form = dialog.querySelector("form");
			form.onsubmit = async (e) => {
				e.preventDefault();
				const fd = new FormData(form);
				const name = fd.get("stream_name").trim();
				if (!name) return;

				const submitBtn = form.querySelector('button[type="submit"]');
				submitBtn.disabled = true;
				try {
					const data = {
						retain_latest: fd.get("retain_latest") === "on",
						retain_latest_max_expiry:
							fd.get("retain_latest_max_expiry").trim() || null,
						auto_expire_previous: fd.get("auto_expire_previous") === "on",
					};

					await API.saveStream(name, data);
					dialog.close();
					window.history.pushState(
						null,
						"",
						`/_/streams/${encodeURIComponent(name)}`,
					);
					window.dispatchEvent(new CustomEvent("artifactory:navigated"));
				} catch (err) {
					showToast(`Failed to create stream: ${err.message}`, "error");
				} finally {
					submitBtn.disabled = false;
				}
			};
		}
		dialog.querySelector("form").reset();
		dialog.showModal();
	};

	return view;
}

async function renderStreamDetail(name) {
	const details = await API.getStreamGroups(name);
	const groups = details.groups || [];
	const view = document.createElement("div");

	view.innerHTML = `
        <nav class="af-breadcrumb" style="display: flex; justify-content: space-between; align-items: center;">
            <div style="display: flex; gap: 8px;">
                <div class="af-breadcrumb-item"><a href="/_/streams" class="af-breadcrumb-link nav-link">📡 Streams</a></div>
                <div class="af-breadcrumb-item"><span class="af-breadcrumb-current">${Format.escapeHtml(name)}</span></div>
            </div>
            <button class="btn btn-primary" id="open-stream-settings-btn" style="font-size: 12px; padding: 4px 10px;">⚙️ Edit Settings</button>
        </nav>

        <div class="af-table-wrapper">
            <table class="af-table af-table-compact af-table-grouped">
                <thead>
                    <tr>
                        <th>Resource / Group</th>
                        <th style="width: 100px;">Size</th>
                        <th style="width: 150px;">Created</th>
                        <th class="af-extras-col"><button class="btn btn-ghost af-extras-eye" title="Expand all extras">👁</button></th>
                        <th style="width: 50px;"></th>
                    </tr>
                </thead>
                <tbody id="grouped-body"></tbody>
            </table>
        </div>
    `;

	let settingsDialog = document.getElementById(
		"manager-stream-settings-dialog",
	);
	if (!settingsDialog) {
		document.body.appendChild(streamSettingsTemplate.content.cloneNode(true));
		settingsDialog = document.getElementById("manager-stream-settings-dialog");

		settingsDialog.querySelectorAll(".modal-close").forEach((btn) => {
			btn.onclick = () => settingsDialog.close();
		});

		const settingsForm = settingsDialog.querySelector("form");
		settingsForm.onsubmit = async (e) => {
			e.preventDefault();
			const btn = settingsForm.querySelector("#save-stream-settings-btn");
			btn.disabled = true;
			try {
				const data = {
					retain_latest: settingsForm.querySelector("#detail-retain-latest")
						.checked,
					retain_latest_max_expiry:
						settingsForm.querySelector("#detail-max-expiry").value.trim() ||
						null,
					auto_expire_previous: settingsForm.querySelector(
						"#detail-auto-expire-previous",
					).checked,
				};
				await API.saveStream(name, data);

				// Update local details cache so reopening modal shows new values
				details.retain_latest = data.retain_latest;
				details.retain_latest_max_expiry = data.retain_latest_max_expiry;
				details.auto_expire_previous = data.auto_expire_previous;

				settingsDialog.close();
				showToast("Stream settings saved successfully!", "success");
			} catch (err) {
				showToast(`Failed to save stream settings: ${err.message}`, "error");
			} finally {
				btn.disabled = false;
			}
		};
	}

	const openBtn = view.querySelector("#open-stream-settings-btn");
	openBtn.onclick = () => {
		settingsDialog.querySelector("#detail-retain-latest").checked =
			!!details.retain_latest;
		settingsDialog.querySelector("#detail-max-expiry").value =
			details.retain_latest_max_expiry || "";
		settingsDialog.querySelector("#detail-auto-expire-previous").checked =
			!!details.auto_expire_previous;
		settingsDialog.showModal();
	};

	initExtrasEye(
		view.querySelector(".af-table"),
		view.querySelector(".af-extras-eye"),
	);

	const tbody = view.querySelector("#grouped-body");

	if (groups === null || groups.length === 0) {
		const groupRow = document.createElement("tr");
		groupRow.className = "af-group-row";
		groupRow.innerHTML = `
            <td colspan="5" class="af-group-header-cell">
                No groups
            </td>
        `;
		tbody.appendChild(groupRow);
	} else {
		const fragment = document.createDocumentFragment();
		groups.forEach((group) => {
			const files = group.files || [];
			// 1. Group Header Row (Using the Badge Visual from FileBrowser)
			const groupRow = document.createElement("tr");
			groupRow.className = "af-group-row";
			groupRow.innerHTML = `
            <td colspan="5" class="af-group-header-cell">
                <span class="badge-origin badge-group">${Format.escapeHtml(name)}/${Format.escapeHtml(group.name)}</span>
                <span class="af-group-badge">${files.length} items</span>
            </td>`;
			fragment.appendChild(groupRow);

			// 2. File Rows
			// Helper to get parent folder from a file path
			function getParentDir(path) {
				if (!path || path === "/") return "/";
				const parts = path.split("/").filter((p) => p);
				if (parts.length <= 1) return "/";
				return `/${parts.slice(0, -1).join("/")}`;
			}

			// Inside the fileRow loop in renderStreamDetail:
			files.forEach((file) => {
				const fileRow = document.createElement("tr");
				fileRow.className = "af-file-row";

				const parentDir = getParentDir(file.path);
				const filename = file.name;

				// file.path, parentDir and filename are user-controlled and may
				// contain characters that need URL encoding (for hrefs) and HTML
				// escaping (for displayed text).
				const absolutePath = Path.toUrl(file.path);
				const locateHref = `${Path.toUrl(parentDir)}?highlight=${encodeURIComponent(filename)}`;
				fileRow.innerHTML = `
                    <td>
                        <div class="af-file-wrapper" style="gap: 8px;">
                            <span class="af-file-icon-container af-no-select" style="position:relative; display:flex; width:24px; height:24px; justify-content:center; align-items:center; flex-shrink:0;">
                                <span class="af-file-icon">${Format.getFileIcon(file)}</span>
                            </span>
                            <a class="${file.isdir ? "nav-link" : ""} af-file-link af-no-select" href="${absolutePath}" ${file.isdir ? "" : 'target="_new"'}>
                                <span class="af-file-text">${Format.escapeHtml(file.path)}</span>
                            </a>
                            <a href="${locateHref}" class="nav-link af-locate-btn" title="Open containing folder">
                                ${ICON_LOCATE} Go to folder
                            </a>
                        </div>
                    </td>
                    <td class="af-col-mono">${file.isdir ? "-" : Format.formatBytes(file.size)}</td>
                    <td class="af-col-mono" style="font-size:11px">${Format.dateTime(file.modTime)}</td>
                    <td class="extras-cell"><div class="af-extras-inner"></div></td>
                    <td>
                        <div class="af-row-actions">
                            <button class="btn btn-ghost info-btn">ℹ️</button>
                        </div>
                    </td>
                `;

				renderExtrasCell(file, fileRow.querySelector(".af-extras-inner"), {
					showStream: false,
				});

				fileRow.querySelector(".info-btn").onclick = () => openFileInfo(file);

				if (!file.isdir && isPreviewable(file)) {
					const link = fileRow.querySelector(".af-file-link");
					link.onclick = (e) => {
						if (e.altKey) {
							e.preventDefault();
							const a = document.createElement("a");
							a.href = absolutePath;
							a.download = filename;
							document.body.appendChild(a);
							a.click();
							document.body.removeChild(a);
							return;
						}
						if (e.ctrlKey || e.metaKey || e.shiftKey) {
							return; // Native modifier behaviors
						}
						e.preventDefault();
						openPreviewDialog(file, absolutePath);
					};
				}

				fragment.appendChild(fileRow);
			});
			tbody.appendChild(fragment);
		});
	}
	return view;
}
