import { API } from "../api/ApiClient.js";
import { Auth } from "../api/Auth.js";
import { Format } from "../api/Format.js";
import { initExtrasEye, renderExtrasCell } from "./ExtrasCell.js";
import { openFileInfo } from "./FileInfo.js";
import { openMoveDialog } from "./MoveDialog.js";
import { isPreviewable, openPreviewDialog } from "./PreviewDialog.js";
import { showToast } from "./Toast.js";
import { openUploadDialog } from "./UploadDialog.js";

const ICON_PARENT = `
<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
    <line x1="12" y1="19" x2="12" y2="5"></line>
    <polyline points="5 12 12 5 19 12"></polyline>
</svg>`;

/**
 * Define the template for the sub page
 * This is parsed only ONCE when the module is loaded.
 */
const template = document.createElement("template");
template.innerHTML = `
    <div class="af-actions" style="display:flex; align-items:center; position: sticky; top: 60px; z-index: 900; background: rgba(248, 249, 250, 0.85); backdrop-filter: blur(8px); padding: 8px 0; margin-bottom: 8px;">
        <div id="batch-actions" class="hidden" style="display:flex; gap:8px; align-items:center; margin-right:16px; padding-right:16px; border-right:1px solid var(--border);">
            <span class="af-batch-count" style="margin-right: 8px;"></span>
            <div class="af-dropdown-container" style="position:relative; display:flex;">
                <button class="btn btn-primary btn-sm" id="batch-download-btn" style="border-top-right-radius: 0; border-bottom-right-radius: 0;">📥 Download</button>
                <button class="btn btn-primary btn-sm af-menu-trigger" id="batch-download-dropdown-btn" style="border-top-left-radius: 0; border-bottom-left-radius: 0; padding-left: 4px; padding-right: 4px; border-left: 1px solid rgba(255,255,255,0.2);">▼</button>
                <div class="af-dropdown hidden" id="batch-download-dropdown" style="top: 100%; right: 0;">
                    <button class="af-dropdown-item" id="batch-download-merged-btn">📥 Download (Merged Mode)</button>
                </div>
            </div>
            <button class="btn btn-danger btn-sm af-requires-auth hidden" id="batch-delete-btn">🗑️ Delete</button>
        </div>
        <button id="new-directory-btn" class="btn btn-ghost" data-af-perms="write">New directory</button>
        <button id="upload-btn" class="btn btn-primary" data-af-perms="write">Upload</button>
    </div>

    <nav class="af-breadcrumb" id="breadcrumb"></nav>

    <div class="af-table-wrapper">
        <table class="af-table af-table-compact af-table-selectable">
            <thead>
                <tr>
                    <th class="sortable" data-sort="name">
                        <div style="display:flex; align-items:center; gap:8px;">
                            <input type="checkbox" id="af-select-all-checkbox" class="af-selection-checkbox" title="Select All">
                            <span>Name <span class="indicator"></span></span>
                        </div>
                    </th>
                    <th class="sortable" data-sort="size" style="width: 100px;">Size <span class="indicator"></span></th>
                    <th class="sortable" data-sort="modtime" style="width: 180px;">Modified <span class="indicator"></span></th>
                    <th class="af-extras-col"><button class="btn btn-ghost af-extras-eye" title="Expand all extras">👁</button></th>
                    <th style="width: 80px;"></th>
                </tr>
            </thead>
            <tbody id="file-list"></tbody>
        </table>
    </div>
`;

/**
 * Define a template for the individual ROWS.
 */
const rowTemplate = document.createElement("template");
rowTemplate.innerHTML = `
    <tr class="af-file-row">
        <td class="name-cell">
            <div class="af-file-wrapper" style="display:flex; align-items:center; gap:8px;">
                <span class="af-file-icon-container af-no-select" style="position:relative; display:flex; width:24px; height:24px; justify-content:center; align-items:center;">
                    <span class="af-file-icon"></span>
                    <span class="af-selection-checkbox-wrapper"><input type="checkbox" class="af-selection-checkbox af-no-select"></span>
                </span>
                <a class="nav-link af-file-link af-no-select">
                    <span class="af-file-text"></span>
                </a>
            </div>
        </td>
        <td class="af-col-mono size-cell"></td>
        <td class="af-col-mono time-cell"></td>
        <td class="extras-cell"><div class="af-extras-inner"></div></td>
        <td class="af-row-actions" style="text-align: right;">
            <div class="af-row-actions-container">
                <button class="btn btn-ghost af-menu-trigger af-no-select" title="Actions">⋮</button>
                
                <div class="af-dropdown hidden af-row-dropdown">
                    <button class="af-dropdown-item info-btn">ℹ️ Details</button>
                    <button class="af-dropdown-item move-btn af-requires-auth">📦 Move</button>
                    <button class="af-dropdown-item edit-btn af-requires-auth">📝 Edit Meta</button>
                    <div class="af-dropdown-divider af-requires-auth"></div>
                    <button class="af-dropdown-item btn-danger del-btn af-requires-auth">🗑️ Delete</button>
                </div>
            </div>
        </td>
    </tr>
`;

const editModalTemplate = document.createElement("template");
editModalTemplate.innerHTML = `
    <dialog class="af-modal">
        <form method="dialog" class="af-form">
            <div class="af-modal-header">
                <h3>Edit File Properties</h3>
                <button type="button" class="btn btn-ghost modal-close">×</button>
            </div>
            <div class="af-modal-body">
                <label>
                    <span>Name</span>
                    <input type="text" name="new_name" id="edit-name-input">
                </label>
                <label>
                    <span>Tags (comma separated)</span>
                    <input type="text" name="tags" placeholder="env=prod, arch=x64">
                </label>
                <label style="display: flex; flex-direction: column; gap: 4px; margin-top: 10px;">
                    <span>Expiration Policy</span>
                    <select name="expiry_type" id="edit-expiry-type" style="padding: 10px; border: 1px solid var(--border); border-radius: var(--radius); font-size: 14px; background: var(--bg-main);">
                        <option value="none">No Expiration</option>
                        <option value="after_upload">Expire After Upload (Relative)</option>
                        <option value="after_download">Expire After Download (Sliding)</option>
                        <option value="absolute">Specific Date/Time (Absolute)</option>
                    </select>
                </label>
                
                <div id="edit-expiry-input-wrapper" style="display: none; margin-top: 10px;">
                    <div id="edit-expiry-duration-wrapper" style="display: none; flex-direction: column; gap: 4px;">
                        <label style="display: flex; flex-direction: column; gap: 4px; margin: 0;">
                            <span>Duration</span>
                            <input type="text" name="expiry_duration" id="edit-expiry-duration" placeholder="e.g. 7d or 24h" style="padding: 10px; border: 1px solid var(--border); border-radius: var(--radius); font-size: 14px;">
                        </label>
                        <small class="af-input-help" style="font-size: 11px; color: var(--text-muted);">Use durations like 7d, 2h, 30m.</small>
                    </div>
                    
                    <div id="edit-expiry-absolute-wrapper" style="display: none; flex-direction: column; gap: 4px;">
                        <label style="display: flex; flex-direction: column; gap: 4px; margin: 0;">
                            <span>Expiration Date</span>
                            <div class="af-input-with-action">
                                <input type="text" name="expiry_absolute" id="edit-expiry-absolute" placeholder="YYYY-MM-DD HH:MM:SS" style="padding: 10px; padding-right: 40px; border: 1px solid var(--border); border-radius: var(--radius); font-size: 14px; width: 100%;">
                                <button type="button" class="af-input-icon-btn" id="edit-expiry-picker-trigger" title="Open Calendar" style="position: absolute; right: 10px; top: 50%; transform: translateY(-50%); background: none; border: none; cursor: pointer; font-size: 16px;">📅</button>
                                <input type="date" id="edit-expiry-hidden-picker" style="opacity: 0; position: absolute; left: 0; top: 0; width: 100%; height: 100%; pointer-events: none;">
                            </div>
                        </label>
                        <small class="af-input-help" style="font-size: 11px; color: var(--text-muted);">YYYY-MM-DD HH:MM:SS format or use the calendar button.</small>
                    </div>
                </div>
                <label style="display: flex; flex-direction: column; gap: 4px; margin-top: 10px;">
                    <span>Stream</span>
                    <div style="display: flex; flex-direction: column; gap: 4px;">
                        <input type="text" name="stream" id="edit-stream-name" style="width: 100%; box-sizing: border-box;">
                        <button type="button" class="btn btn-ghost" id="edit-stream-configure-btn" style="display: none; align-self: flex-start; font-size: 11px; padding: 4px 8px; min-height: 24px; height: 24px; margin-top: 2px;">⚙️ Update Stream</button>
                    </div>
                </label>
                <label style="display: flex; flex-direction: column; gap: 4px; margin-top: 10px;">
                    <span>Group</span>
                    <input type="text" name="group" id="edit-group-name" style="width: 100%; box-sizing: border-box;">
                </label>
                <div id="edit-stream-validation" class="af-alert af-alert-error" style="display: none; padding: 10px; margin-top: -6px;">
                    <strong>⚠️ Required:</strong> Both Stream Name and Group Name must be provided.
                </div>
                <div id="edit-stream-group-note" class="af-alert af-alert-warning" style="display: none; padding: 10px; margin-top: -6px;">
                    Stream/group cannot be changed: this stream has <strong>auto-expire-previous</strong> enabled.
                </div>

                <label class="af-check-group" style="margin-top: 10px;">
                    <input type="checkbox" name="immutable"> 
                    <span>Immutable</span>
                </label>

                <!-- Directory Specific Fields -->
                <div class="af-dir-only" style="margin-top: 15px; padding-top: 15px; border-top: 1px dashed var(--border)">
                    <label class="af-check-group" style="margin-bottom: 4px;">
                        <input type="checkbox" name="auto_prune">
                        <span>Auto Prune</span>
                    </label>
                    <small class="af-input-help" style="display: block; margin-left: 24px; margin-bottom: 10px;">Remove this directory when it becomes empty.</small>
                    
                    <label class="af-check-group" style="margin-bottom: 4px;">
                        <input type="checkbox" name="prune_children">
                        <span>Prune Descendants</span>
                    </label>
                    <small class="af-input-help" style="display: block; margin-left: 24px;">Recursively remove any descendant directory which becomes empty, at any depth.</small>
                </div>
            </div>
            <div class="af-modal-footer">
                <button type="button" class="btn btn-ghost modal-close">Cancel</button>
                <button type="submit" class="btn btn-primary" id="save-btn">Save Changes</button>
            </div>
        </form>
    </dialog>
`;

const editStreamSettingsTemplate = document.createElement("template");
editStreamSettingsTemplate.innerHTML = `
    <dialog class="af-modal" id="edit-stream-settings-dialog" style="max-width: 400px;">
        <form class="af-form">
            <div class="af-modal-header">
                <h3>Stream Settings</h3>
                <button type="button" class="btn btn-ghost modal-close">×</button>
            </div>
            <div class="af-modal-body">
                <div style="margin-bottom: 15px;">
                    <label class="af-check-group">
                        <input type="checkbox" name="stream_retain_latest" id="edit-stream-retain-latest"> 
                        <span>Retain Latest Group</span>
                    </label>
                    <small class="af-input-help" style="margin-left: 28px; display: block;">The latest group's expiry is paused/inactive, keeping it alive.</small>
                </div>
                <div style="margin-bottom: 15px;">
                    <label style="display: flex; flex-direction: column; gap: 4px;">
                        <span>Latest Group Max Expiry</span>
                        <input type="text" name="stream_retain_latest_max_expiry" id="edit-stream-retain-latest-max-expiry" placeholder="e.g. 90d" style="padding: 10px; border: 1px solid var(--border); border-radius: var(--radius); font-size: 14px;">
                    </label>
                    <small class="af-input-help" style="display: block; margin-top: 4px;">If "Retain Latest" is on, this forces the latest group to eventually expire.</small>
                </div>
                <div style="margin-bottom: 15px;">
                    <label class="af-check-group">
                        <input type="checkbox" name="stream_auto_expire_previous" id="edit-stream-auto-expire-previous"> 
                        <span>Auto Expire Previous Groups</span>
                    </label>
                    <small class="af-input-help" style="margin-left: 28px; display: block;">When a new group is uploaded, all previous groups expire immediately.</small>
                </div>
            </div>
            <div class="af-modal-footer">
                <button type="button" class="btn btn-ghost modal-close">Cancel</button>
                <button type="submit" class="btn btn-primary" id="save-edit-stream-settings-btn">Save Settings</button>
            </div>
        </form>
    </dialog>
`;

let originalMeta;
let isSelectionMode = false;
const selectedPaths = new Set();
let preventClick = false;
let lastSelectedIndex = -1;

function openEditModal(file, path) {
	const dialog = getDialogNode("edit-meta-dialog", editModalTemplate);
	if (!dialog.dataset.bound) {
		dialog.querySelector("form").onsubmit = onEditSubmit;
		dialog
			.querySelectorAll(".modal-close")
			.forEach((btn) => (btn.onclick = () => dialog.close()));

		// Expiry UI setup
		const expiryTypeSelect = dialog.querySelector("#edit-expiry-type");
		const expiryInputWrapper = dialog.querySelector(
			"#edit-expiry-input-wrapper",
		);
		const expiryDurationWrapper = dialog.querySelector(
			"#edit-expiry-duration-wrapper",
		);
		const expiryAbsoluteWrapper = dialog.querySelector(
			"#edit-expiry-absolute-wrapper",
		);
		expiryTypeSelect.onchange = () => {
			const type = expiryTypeSelect.value;
			if (type === "none") {
				expiryInputWrapper.style.display = "none";
				expiryDurationWrapper.style.display = "none";
				expiryAbsoluteWrapper.style.display = "none";
			} else if (type === "after_upload" || type === "after_download") {
				expiryInputWrapper.style.display = "block";
				expiryDurationWrapper.style.display = "flex";
				expiryAbsoluteWrapper.style.display = "none";
			} else if (type === "absolute") {
				expiryInputWrapper.style.display = "block";
				expiryDurationWrapper.style.display = "none";
				expiryAbsoluteWrapper.style.display = "flex";
			}
		};

		const absoluteInput = dialog.querySelector("#edit-expiry-absolute");
		const hiddenPicker = dialog.querySelector("#edit-expiry-hidden-picker");
		const trigger = dialog.querySelector("#edit-expiry-picker-trigger");
		hiddenPicker.addEventListener("input", () => {
			if (hiddenPicker.value) {
				absoluteInput.value = `${hiddenPicker.value} 23:59:59`;
				absoluteInput.classList.add("af-highlight-input");
				setTimeout(
					() => absoluteInput.classList.remove("af-highlight-input"),
					500,
				);
				absoluteInput.dispatchEvent(new Event("input", { bubbles: true }));
			}
		});
		trigger.onclick = (e) => {
			e.preventDefault();
			if (typeof hiddenPicker.showPicker === "function") {
				hiddenPicker.showPicker();
			} else {
				hiddenPicker.click();
			}
		};

		dialog.dataset.bound = "true";
	}

	const settingsDialog = getDialogNode(
		"edit-stream-settings-dialog",
		editStreamSettingsTemplate,
	);

	const form = dialog.querySelector("form");
	const isDir = file.isdir;

	dialog.querySelectorAll(".af-dir-only").forEach((el) => {
		el.classList.toggle("hidden", !isDir);
	});

	// Store the "Original" state for comparison later
	// We normalize the values (e.g., dates) to match how they appear in form inputs
	originalMeta = {
		new_name: file.name,
		tags: file.tags
			? file.tags
					.map((e) => (e.value ? `${e.key}=${e.value}` : e.key))
					.join(", ")
			: "",
		immutable: file.policy.is_immutable || false,
		expires: file.retention?.expires || null,
		stream: file.stream || "",
		group: file.group || "",
		auto_prune: file.retention?.auto_prune || false,
		prune_children: file.retention?.prune_children || false,
	};

	form.dataset.path = `${path}/${file.name}`;

	// Populate the UI

	const new_name = form.querySelector('[name="new_name"]');
	new_name.value = originalMeta.new_name;
	const reasons = [];
	if (file.policy.is_immutable) reasons.push("Locked (Immutable)");
	if (file.policy.is_protected) reasons.push("Protected Path");
	if (!file.policy.is_allowed) reasons.push("Outside your scope");
	new_name.disabled = reasons.length > 0;
	if (new_name.disabled) new_name.title = reasons[0];

	form.querySelector('[name="tags"]').value = originalMeta.tags;

	const expiryTypeSelect = dialog.querySelector("#edit-expiry-type");
	const expiryDurationInput = dialog.querySelector("#edit-expiry-duration");
	const expiryAbsoluteInput = dialog.querySelector("#edit-expiry-absolute");

	if (originalMeta.expires) {
		if (originalMeta.expires.after_upload) {
			expiryTypeSelect.value = "after_upload";
			expiryDurationInput.value = originalMeta.expires.after_upload;
		} else if (originalMeta.expires.after_download) {
			expiryTypeSelect.value = "after_download";
			expiryDurationInput.value = originalMeta.expires.after_download;
		} else if (originalMeta.expires.at) {
			expiryTypeSelect.value = "absolute";
			expiryAbsoluteInput.value = Format.toHTMLInput(originalMeta.expires.at);
		}
	} else {
		expiryTypeSelect.value = "none";
		expiryDurationInput.value = "";
		expiryAbsoluteInput.value = "";
	}
	expiryTypeSelect.dispatchEvent(new Event("change"));

	form.querySelector('[name="immutable"]').checked = originalMeta.immutable;
	const streamNameInput = dialog.querySelector("#edit-stream-name");
	const configureBtn = dialog.querySelector("#edit-stream-configure-btn");
	streamNameInput.value = originalMeta.stream;
	form.querySelector('[name="group"]').value = originalMeta.group;
	form.querySelector('[name="auto_prune"]').checked = !!originalMeta.auto_prune;
	form.querySelector('[name="prune_children"]').checked =
		!!originalMeta.prune_children;

	let debounceTimer;

	if (!settingsDialog.dataset.bound) {
		settingsDialog.querySelectorAll(".modal-close").forEach((btn) => {
			btn.onclick = () => settingsDialog.close();
		});

		settingsDialog.querySelector(".af-form").onsubmit = async (e) => {
			e.preventDefault();
			const streamName = streamNameInput.value.trim();

			currentStreamSettings.retain_latest = settingsDialog.querySelector(
				"#edit-stream-retain-latest",
			).checked;
			currentStreamSettings.retain_latest_max_expiry = settingsDialog
				.querySelector("#edit-stream-retain-latest-max-expiry")
				.value.trim();
			currentStreamSettings.auto_expire_previous = settingsDialog.querySelector(
				"#edit-stream-auto-expire-previous",
			).checked;

			if (streamName) {
				try {
					await API.saveStream(streamName, {
						retain_latest: currentStreamSettings.retain_latest,
						retain_latest_max_expiry:
							currentStreamSettings.retain_latest_max_expiry || null,
						auto_expire_previous: currentStreamSettings.auto_expire_previous,
					});
					configureBtn.classList.add("btn-success");
					setTimeout(() => configureBtn.classList.remove("btn-success"), 1000);
				} catch (err) {
					showToast(`Failed to save stream settings: ${err.message}`, "error");
				}
			}

			settingsDialog.close();
		};

		settingsDialog.dataset.bound = "true";
	}

	const currentStreamSettings = {
		retain_latest: false,
		retain_latest_max_expiry: "",
		auto_expire_previous: false,
	};

	const triggerStreamCheck = () => {
		clearTimeout(debounceTimer);
		const name = streamNameInput.value.trim();
		if (!name) {
			configureBtn.style.display = "none";
			return;
		}

		configureBtn.style.display = "block";
		configureBtn.textContent = "Loading...";
		configureBtn.disabled = true;

		debounceTimer = setTimeout(async () => {
			try {
				const details = await API.getStreamGroups(name);
				if (details) {
					configureBtn.textContent = "⚙️ Update existing stream";
					configureBtn.disabled = false;
					currentStreamSettings.retain_latest = !!details.retain_latest;
					currentStreamSettings.retain_latest_max_expiry =
						details.retain_latest_max_expiry || "";
					currentStreamSettings.auto_expire_previous =
						!!details.auto_expire_previous;
				}
			} catch (err) {
				if (err.status === 404) {
					configureBtn.textContent = "✨ Configure new stream";
					configureBtn.disabled = false;
					currentStreamSettings.retain_latest = false;
					currentStreamSettings.retain_latest_max_expiry = "";
					currentStreamSettings.auto_expire_previous = false;
				} else {
					console.error("Error fetching stream details:", err);
					configureBtn.textContent = "⚠️ Error";
				}
			}
			updateStreamGroupLock();
		}, 300);
	};

	streamNameInput.oninput = () => {
		dialog.querySelector("#edit-stream-validation").style.display = "none";
		triggerStreamCheck();
	};

	const groupInput = dialog.querySelector("#edit-group-name");
	if (groupInput) {
		groupInput.oninput = () => {
			dialog.querySelector("#edit-stream-validation").style.display = "none";
		};
	}

	const streamGroupNote = dialog.querySelector("#edit-stream-group-note");

	const updateStreamGroupLock = () => {
		if (!originalMeta.stream) return;
		const locked = currentStreamSettings.auto_expire_previous;
		streamNameInput.disabled = locked;
		if (groupInput) groupInput.disabled = locked;
		streamGroupNote.style.display = locked ? "block" : "none";
		dialog.dataset.autoExpirePrevious = locked ? "true" : "false";
	};

	// Default: assume safe until stream check resolves
	dialog.dataset.autoExpirePrevious = "false";

	if (streamNameInput.value) {
		triggerStreamCheck();
	} else {
		configureBtn.style.display = "none";
	}

	dialog.querySelector("#edit-stream-configure-btn").onclick = () => {
		settingsDialog.querySelector("#edit-stream-retain-latest").checked =
			currentStreamSettings.retain_latest;
		settingsDialog.querySelector(
			"#edit-stream-retain-latest-max-expiry",
		).value = currentStreamSettings.retain_latest_max_expiry;
		settingsDialog.querySelector("#edit-stream-auto-expire-previous").checked =
			currentStreamSettings.auto_expire_previous;
		settingsDialog.showModal();
	};

	dialog.showModal();
}

async function onEditSubmit(e) {
	// dialog will be closed at the end if everything is ok
	e.preventDefault();

	const dialog = document.getElementById("edit-meta-dialog");
	const form = dialog.querySelector("form");
	const fd = new FormData(form);
	let currentPath = form.dataset.path;
	const parentPath = currentPath.substring(0, currentPath.lastIndexOf("/"));

	const newName = fd.get("new_name");
	const hasRenamed = newName && newName !== originalMeta.new_name;
	try {
		if (hasRenamed) {
			await API.renameResource(currentPath, newName);
			// Update the path for the next step
			currentPath = `${parentPath}/${newName}`.replace(/\/+/g, "/");
		}
	} catch (err) {
		showToast(`Failed to save: ${err.message}`, "error");
	}
	const payload = {};

	const currentTags = fd.get("tags");
	if (currentTags !== originalMeta.tags) payload.tags = currentTags;

	const currentImmutable = fd.get("immutable") === "on";
	if (currentImmutable !== originalMeta.immutable) {
		payload.retention = payload.retention || {};
		payload.retention.immutable = currentImmutable;
	}

	const expiryType = fd.get("expiry_type");
	let newExpires = null;
	if (expiryType === "after_upload") {
		const val = fd.get("expiry_duration")?.trim();
		if (val) newExpires = { after_upload: val };
	} else if (expiryType === "after_download") {
		const val = fd.get("expiry_duration")?.trim();
		if (val) newExpires = { after_download: val };
	} else if (expiryType === "absolute") {
		const val = fd.get("expiry_absolute")?.trim();
		if (val) {
			const date = new Date(val.replace(" ", "T"));
			if (!Number.isNaN(date.getTime()) && date < new Date()) {
				showToast("Expiry date cannot be in the past.", "error");
				return;
			}
			newExpires = { at: val };
		}
	} else if (expiryType === "none") {
		newExpires = {}; // Empty object clears expiration
	}

	// Compare with originalMeta.expires
	const origExpStr = originalMeta.expires
		? JSON.stringify(originalMeta.expires)
		: null;
	const newExpStr =
		newExpires && Object.keys(newExpires).length > 0
			? JSON.stringify(newExpires)
			: newExpires && Object.keys(newExpires).length === 0
				? "{}"
				: null;

	if (newExpires && origExpStr !== newExpStr) {
		payload.retention = payload.retention || {};
		payload.retention.expires = newExpires;
	}

	const currentStream = fd.get("stream")?.trim();
	const currentGroup = fd.get("group")?.trim();
	const validationMsg = dialog.querySelector("#edit-stream-validation");

	if ((currentStream && !currentGroup) || (!currentStream && currentGroup)) {
		validationMsg.style.display = "block";
		return;
	}
	validationMsg.style.display = "none";

	const autoExpirePrevious = dialog.dataset.autoExpirePrevious === "true";
	const canChangeGroup = !originalMeta.stream || !autoExpirePrevious;
	if (
		canChangeGroup &&
		(currentStream !== originalMeta.stream ||
			currentGroup !== originalMeta.group)
	) {
		payload.stream = currentStream;
		payload.group = currentGroup;
	}

	const currentAutoPrune = fd.get("auto_prune") === "on";
	if (currentAutoPrune !== !!originalMeta.auto_prune) {
		payload.retention = payload.retention || {};
		payload.retention.auto_prune = currentAutoPrune;
	}

	const currentPruneChildren = fd.get("prune_children") === "on";
	if (currentPruneChildren !== !!originalMeta.prune_children) {
		payload.retention = payload.retention || {};
		payload.retention.prune_children = currentPruneChildren;
	}

	if (Object.keys(payload).length !== 0) {
		try {
			await API.patchEntry(currentPath, payload);
		} catch (err) {
			showToast(`Failed to patch file entry: ${err.message}`, "error");
		}
	}

	dialog.close();
	form.reset();
	window.dispatchEvent(
		new CustomEvent("artifactory:refresh", {
			detail: { path: window.location.pathname },
		}),
	);
}

function getDialogNode(id, template) {
	let dialog = document.getElementById(id);
	if (!dialog) {
		const node = template.content.cloneNode(true);
		node.children[0].id = id;
		document.body.appendChild(node);
		dialog = document.getElementById(id);
	}

	// need to reinitialize hooks for when view is recreated
	dialog.querySelectorAll(".modal-close").forEach((btn) => {
		btn.onclick = () => dialog.close();
	});

	return dialog;
}

function updateSearchParams(col, nextOrder) {
	const newUrl = new URL(window.location.href);
	localStorage.setItem("af_pref_sort", col);
	localStorage.setItem("af_pref_order", nextOrder);

	const p = newUrl.searchParams;
	let changes = false;
	if (col === "name" && nextOrder === "asc") {
		// remove the default
		changes = p.has("sort") || p.has("order");
		p.delete("sort");
		p.delete("order");
	} else {
		changes = !p.has("sort", col) || !p.has("order", nextOrder);
		p.set("sort", col);
		p.set("order", nextOrder);
	}

	// Use replaceState so this sync doesn't create a "ghost" back-step
	if (!changes) {
		return;
	}
	window.history.replaceState({}, "", newUrl);
}

function enterSelectionMode(table) {
	isSelectionMode = true;
	lastSelectedIndex = -1;
	table.classList.add("af-table-selection-mode", "af-selection-active");
}

function exitSelectionMode() {
	isSelectionMode = false;
	lastSelectedIndex = -1;
	selectedPaths.clear();
	document
		.querySelector(".af-table")
		?.classList.remove("af-table-selection-mode");
	document
		.querySelectorAll(".af-file-row")
		.forEach((r) => r.classList.remove("is-selected"));
	document
		.querySelectorAll(".af-selection-checkbox")
		.forEach((cb) => (cb.checked = false));
	updateBatchBar();
}

function updateBatchBar() {
	const bar = document.getElementById("batch-actions");
	if (!bar) return;

	const count = selectedPaths.size;
	const countEl = bar.querySelector(".af-batch-count");
	countEl.textContent = count > 0 ? `${count} selected` : "";

	const delBtn = document.getElementById("batch-delete-btn");
	if (Auth.isLoggedIn()) {
		bar.classList.toggle("hidden", count === 0);
		if (delBtn) delBtn.classList.toggle("hidden", count === 0);
	} else {
		bar.classList.remove("hidden");
		if (delBtn) delBtn.classList.add("hidden");
		const dlBtn = document.getElementById("batch-download-btn");
		const dlDropBtn = document.getElementById("batch-download-dropdown-btn");
		if (dlBtn) dlBtn.disabled = count === 0;
		if (dlDropBtn) dlDropBtn.disabled = count === 0;
	}

	const selectAllCb = document.getElementById("af-select-all-checkbox");
	if (selectAllCb) {
		const visibleRows = document.querySelectorAll(
			".af-file-row:not(.af-back-row)",
		);
		let checkedCount = 0;
		visibleRows.forEach((row) => {
			const cb = row.querySelector(".af-selection-checkbox");
			if (cb?.checked) checkedCount++;
		});
		selectAllCb.checked =
			count > 0 &&
			checkedCount === visibleRows.length &&
			visibleRows.length > 0;
		selectAllCb.indeterminate =
			checkedCount > 0 && checkedCount < visibleRows.length;
	}
}

export async function FileBrowser(path) {
	// Clear selection on navigation
	selectedPaths.clear();
	isSelectionMode = false;
	lastSelectedIndex = -1;
	const handleDelete = async (file) => {
		const fullPath = `${path}/${file.name}`.replace(/\/+/g, "/");

		if (confirm(`Are you sure you want to delete ${file.name}?`)) {
			if (file.isdir) {
				if (
					!confirm(
						"You are about to delete a directory. All content will be removed recursively.\n\nContinue?",
					)
				) {
					return;
				}
			}

			try {
				await API.deleteFile(fullPath);

				// Success: Dispatch event to refresh the UI
				window.dispatchEvent(
					new CustomEvent("artifactory:refresh", {
						detail: { path },
					}),
				);
			} catch (err) {
				showToast(`Delete failed: ${err.message}`, "error");
			}
		}
	};

	const urlParams = new URLSearchParams(window.location.search);
	const highlightName = urlParams.get("highlight");

	// Sort state
	// Priority: URL (highest) > localStorage (persisted) > Defaults
	const sortBy =
		urlParams.get("sort") || localStorage.getItem("af_pref_sort") || "name";
	const sortOrder =
		urlParams.get("order") || localStorage.getItem("af_pref_order") || "asc";
	updateSearchParams(sortBy, sortOrder);

	const files = await API.listFiles(path);

	if (!files || files.length === undefined) {
		const container = document.createElement("div");
		container.innerHTML = `
            <div class="af-card">
                Not a directory
            </div>
        `;
		return container;
	}

	// Sorting Logic
	files.sort((a, b) => {
		// Rule 1: Directories always first
		if (a.isdir !== b.isdir) return a.isdir ? -1 : 1;

		// Rule 2: Sort by selected column
		let valA = sortBy === "modtime" ? a.modTime : a[sortBy];
		let valB = sortBy === "modtime" ? b.modTime : b[sortBy];

		// Handle types (strings vs numbers vs dates)
		if (sortBy === "modtime") {
			valA = new Date(valA);
			valB = new Date(valB);
		} else if (sortBy === "name") {
			valA = valA.toLowerCase();
			valB = valB.toLowerCase();
		}

		if (valA < valB) return sortOrder === "asc" ? -1 : 1;
		if (valA > valB) return sortOrder === "asc" ? 1 : -1;
		return 0;
	});

	const content = template.content.cloneNode(true);

	const breadcrumbNav = content.getElementById("breadcrumb");
	breadcrumbNav.innerHTML = renderBreadcrumbs(path);

	// New directory
	const newFolderBtn = content.getElementById("new-directory-btn");
	newFolderBtn.onclick = async () => {
		const folderName = prompt("Enter directory name:");
		if (!folderName) return;

		const fullPath = `${path}/${folderName}`.replace(/\/+/g, "/");
		try {
			await API.createDirectory(fullPath);
			window.dispatchEvent(
				new CustomEvent("artifactory:refresh", {
					detail: { path },
				}),
			);
		} catch (err) {
			showToast(err.message, "error");
		}
	};

	// Edit dialog
	const editDialog = getDialogNode("edit-meta-dialog", editModalTemplate);
	editDialog.querySelector("form").onsubmit = onEditSubmit;

	// Upload functionality
	const uploadBtn = content.querySelector("#upload-btn");
	uploadBtn.onclick = () => openUploadDialog(path);

	// Batch actions functionality
	const batchDownloadBtn = content.querySelector("#batch-download-btn");
	const batchDownloadDropdownBtn = content.querySelector(
		"#batch-download-dropdown-btn",
	);
	const batchDownloadDropdown = content.querySelector(
		"#batch-download-dropdown",
	);
	const batchDownloadMergedBtn = content.querySelector(
		"#batch-download-merged-btn",
	);
	const selectAllCheckbox = content.querySelector("#af-select-all-checkbox");

	window.exitSelectionMode = exitSelectionMode;

	batchDownloadBtn.onclick = () => {
		const params = Array.from(selectedPaths)
			.map((p) => `p=${encodeURIComponent(p)}`)
			.join("&");
		window.location.href = `/_/api/v1/batch?${params}`;
	};
	batchDownloadMergedBtn.onclick = () => {
		const params = Array.from(selectedPaths)
			.map((p) => `p=${encodeURIComponent(p)}`)
			.join("&");
		window.location.href = `/_/api/v1/batch?mode=merge&${params}`;
	};
	batchDownloadDropdownBtn.onclick = (e) => {
		e.stopPropagation();
		batchDownloadDropdown.classList.toggle("hidden");
		if (!batchDownloadDropdown.classList.contains("hidden")) {
			document.addEventListener(
				"click",
				() => batchDownloadDropdown.classList.add("hidden"),
				{ once: true },
			);
		}
	};

	const batchDeleteBtn = content.querySelector("#batch-delete-btn");
	batchDeleteBtn.onclick = async () => {
		const count = selectedPaths.size;
		if (count === 0) return;
		if (
			!confirm(
				`Delete ${count} item${count !== 1 ? "s" : ""}? Directories will be deleted recursively.\n\nThis cannot be undone.`,
			)
		) {
			return;
		}
		try {
			const result = await API.batchDelete(Array.from(selectedPaths));
			const errors = result?.errors ?? {};
			const errorCount = Object.keys(errors).length;

			if (result.deleted.length > 0) {
				showToast(
					`Deleted ${result.deleted.length} item${result.deleted.length !== 1 ? "s" : ""}.`,
					"success",
				);
			}
			if (errorCount > 0) {
				// The failing paths are user-supplied, so they are passed as
				// structured detail lines rather than interpolated into markup.
				const lines = Object.entries(errors).map(([p, msg]) => `${p} — ${msg}`);
				showToast(
					`Failed to delete ${errorCount} item${errorCount !== 1 ? "s" : ""}:`,
					"error",
					null,
					0,
					lines,
				);
			}
			exitSelectionMode();
			window.dispatchEvent(
				new CustomEvent("artifactory:refresh", { detail: { path } }),
			);
		} catch (err) {
			showToast(`Batch delete failed: ${err.message}`, "error", null, 0);
		}
	};

	selectAllCheckbox.onclick = (e) => {
		e.stopPropagation();
		const checked = e.target.checked;
		const rows = document.querySelectorAll(".af-file-row:not(.af-back-row)");
		rows.forEach((r) => {
			const cb = r.querySelector(".af-selection-checkbox");
			if (cb && cb.checked !== checked) {
				cb.click();
			}
		});
		if (checked && !isSelectionMode) {
			const t = document.querySelector(".af-table");
			if (t) enterSelectionMode(t);
		}
	};

	// Extras eye toggle
	initExtrasEye(
		content.querySelector(".af-table"),
		content.querySelector(".af-extras-eye"),
	);

	// Update Header Indicators
	content.querySelectorAll("th.sortable").forEach((th) => {
		const col = th.dataset.sort;
		if (col === sortBy) {
			th.querySelector(".indicator").textContent =
				sortOrder === "asc" ? " ↑" : " ↓";
			th.classList.add("active-sort");
		}

		th.onclick = () => {
			const nextOrder = col === sortBy && sortOrder === "asc" ? "desc" : "asc";
			updateSearchParams(col, nextOrder);

			// Re-render the app
			window.dispatchEvent(new CustomEvent("artifactory:navigated"));
		};
	});

	// prepare file list
	const listBody = content.querySelector("#file-list");
	const fragment = document.createDocumentFragment();

	if (path !== "/") {
		const row = rowTemplate.content.cloneNode(true);
		const tr = row.querySelector(".af-file-row");
		const link = row.querySelector(".af-file-link");
		const icon = row.querySelector(".af-file-icon");
		const text = row.querySelector(".af-file-text");

		tr.classList.add("af-back-row", "af-no-select");
		icon.innerHTML = ICON_PARENT;
		const checkboxWrapper = row.querySelector(".af-selection-checkbox-wrapper");
		if (checkboxWrapper) checkboxWrapper.remove();
		const parent = path.substring(
			0,
			path.lastIndexOf("/", path.length - (path.endsWith("/") ? 2 : 1)) + 1,
		);
		text.textContent = "..";
		link.href = parent;

		row.querySelector(".size-cell").textContent = "--";
		row.querySelector(".time-cell").textContent = "";
		row.querySelector(".af-row-actions").innerHTML = "";

		const backNameCell = row.querySelector(".name-cell");
		if (backNameCell) {
			backNameCell.style.cursor = "pointer";
			backNameCell.onclick = (e) => {
				if (e.target !== link && !link.contains(e.target)) {
					e.stopPropagation();
					link.click();
				}
			};
		}

		fragment.appendChild(row);
	}

	const table = content.querySelector(".af-table");
	if (selectedPaths.size > 0) {
		isSelectionMode = true;
		table.classList.add("af-table-selection-mode");
	}

	if (!Auth.isLoggedIn()) {
		const batchActions = content.querySelector("#batch-actions");
		batchActions.classList.remove("hidden");
		batchActions.style.borderRight = "none";
		batchActions.style.marginRight = "0";
		batchActions.style.paddingRight = "0";
		const hasSelection = selectedPaths.size > 0;
		content.querySelector("#batch-download-btn").disabled = !hasSelection;
		content.querySelector("#batch-download-dropdown-btn").disabled =
			!hasSelection;
	}

	files.forEach((file, index) => {
		const row = rowTemplate.content.cloneNode(true);
		const el = row.querySelector(".af-file-row");
		const link = row.querySelector(".af-file-link");
		const icon = row.querySelector(".af-file-icon");
		const text = row.querySelector(".af-file-text");

		el.dataset.index = index;

		// Set Icon
		icon.textContent = Format.getFileIcon(file);

		// Set Name and Link
		text.textContent = file.name;
		const fullPath = `${path}/${encodeURIComponent(file.name)}`.replace(
			/\/+/g,
			"/",
		);
		link.href = fullPath;
		if (!file.isdir) {
			if (isPreviewable(file)) {
				link.onclick = (e) => {
					if (e.altKey) {
						e.preventDefault();
						const a = document.createElement("a");
						a.href = fullPath;
						a.download = file.name;
						document.body.appendChild(a);
						a.click();
						document.body.removeChild(a);
						return;
					}
					if (e.ctrlKey || e.metaKey || e.shiftKey) {
						// Let the browser handle native modifier clicks (new tab/window)
						return;
					}
					e.preventDefault();
					openPreviewDialog(file, fullPath);
				};
			} else {
				link.target = "_new";
			}
			link.classList.remove("nav-link");
		}

		row.querySelector(".size-cell").textContent = file.isdir
			? "--"
			: Format.formatBytes(file.size);
		row.querySelector(".time-cell").textContent = Format.dateTime(file.modTime);

		// Extras cell (stream, tags, expiry)
		renderExtrasCell(file, row.querySelector(".af-extras-inner"));

		if (highlightName && file.name === highlightName) {
			const tr = row.querySelector(".af-file-row");
			tr.classList.add("af-highlight-row");

			// Scroll into view after a tiny delay to ensure DOM rendering is complete
			setTimeout(() => {
				tr.scrollIntoView({ behavior: "smooth", block: "center" });
			}, 150);

			// Optional: Remove the parameter from the URL after a few seconds
			// so refreshing doesn't keep highlighting forever
			setTimeout(() => {
				const cleanUrl = new URL(window.location.href);
				cleanUrl.searchParams.delete("highlight");
				window.history.replaceState(
					null,
					"",
					cleanUrl.pathname + cleanUrl.search,
				);
			}, 3000);
		}

		// Actions
		// Inside the file rendering loop
		const menuTrigger = row.querySelector(".af-menu-trigger");
		const dropdown = row.querySelector(".af-row-dropdown");

		menuTrigger.onclick = (e) => {
			e.stopPropagation(); // Prevents clicking the row itself

			// Close any other open menus first
			document.querySelectorAll(".af-row-dropdown").forEach((d) => {
				if (d !== dropdown) d.classList.add("hidden");
			});

			dropdown.classList.toggle("hidden");

			// Auto-close when clicking anywhere else
			if (!dropdown.classList.contains("hidden")) {
				document.addEventListener(
					"click",
					() => dropdown.classList.add("hidden"),
					{ once: true },
				);
			}
		};

		// Hook up the buttons inside the menu
		row.querySelector(".info-btn").onclick = () => openFileInfo(file);
		row.querySelector(".move-btn").onclick = () => openMoveDialog(file, path);
		row.querySelector(".edit-btn").onclick = () => openEditModal(file, path);
		row.querySelector(".del-btn").onclick = () => handleDelete(file);

		// Policy indicator
		const policy = file.policy;
		if (policy.is_immutable || policy.is_protected || !policy.is_allowed) {
			// The tooltip explains exactly why
			const reasons = [];
			if (policy.is_immutable) reasons.push("Locked (Immutable)");
			if (policy.is_protected) reasons.push("Protected Path");
			if (!policy.is_allowed) reasons.push("Outside your scope");

			const iconContainer = row.querySelector(".af-file-icon-container");
			if (iconContainer)
				iconContainer.title = `Restrictions active: ${reasons.join(", ")}`;

			if (!policy.is_allowed) icon.classList.add("af-icon-restricted");
			else if (policy.is_protected || policy.is_immutable)
				icon.classList.add("af-icon-protected");

			[row.querySelector(".del-btn"), row.querySelector(".move-btn")].forEach(
				(btn) => {
					btn.disabled = true;
					btn.title = `Delete disabled: ${reasons[0]}`;
				},
			);
		}

		// selection mode
		{
			const checkbox = row.querySelector(".af-selection-checkbox");
			const rowEl = row.querySelector(".af-file-row");
			const fullPath = `${path}/${file.name}`.replace(/\/+/g, "/");

			// If this specific file was already selected, restore its visual state
			if (selectedPaths.has(fullPath)) {
				checkbox.checked = true;
				rowEl.classList.add("is-selected");
			}

			const toggleItem = (row, force, setLast = true) => {
				const file = row.querySelector(".af-file-text").textContent;
				const checkbox = row.querySelector(".af-selection-checkbox");
				const fullPath = `${path}/${file}`.replace(/\/+/g, "/");
				const newState = force !== undefined ? force : !checkbox.checked;
				checkbox.checked = newState;

				if (newState) {
					selectedPaths.add(fullPath);
					row.classList.add("is-selected");
					if (!isSelectionMode) enterSelectionMode(table);
				} else {
					selectedPaths.delete(fullPath);
					row.classList.remove("is-selected");
				}

				// If user unchecks the last item, exit selection mode automatically
				if (selectedPaths.size === 0 && isSelectionMode) {
					exitSelectionMode();
				}

				updateBatchBar();
				// Only update the anchor when selecting; deselecting leaves it for the caller to adjust.
				if (setLast && newState)
					lastSelectedIndex = parseInt(row.dataset.index, 10);
			};

			const nameCell = row.querySelector(".name-cell");
			if (nameCell) {
				nameCell.style.cursor = "pointer";
				nameCell.onclick = (e) => {
					if (isSelectionMode) return;
					// Modifier clicks are handled by rowEl.onclick; don't simulate link.click()
					if (e.ctrlKey || e.metaKey || e.shiftKey) return;
					if (
						e.target.closest(".af-no-select") &&
						!e.target.closest(".af-file-link")
					)
						return;

					const link = nameCell.querySelector(".af-file-link");
					if (link && e.target !== link && !link.contains(e.target)) {
						e.stopPropagation();
						link.click();
					}
				};
			}

			rowEl.onclick = (e) => {
				if (preventClick) {
					preventClick = false;
					e.preventDefault();
					e.stopPropagation();
					return;
				}

				// Dropdown action items have no stopPropagation — skip them explicitly.
				if (e.target.closest(".af-row-dropdown")) return;

				const currentIndex = parseInt(rowEl.dataset.index, 10);

				// CTRL/CMD+click: toggle individual item, enter selection mode if needed.
				if (e.ctrlKey || e.metaKey) {
					e.preventDefault();
					e.stopPropagation();
					const wasSelected = rowEl.classList.contains("is-selected");
					toggleItem(rowEl);
					if (wasSelected && selectedPaths.size > 0) {
						// After deselecting, move the anchor to the highest-index still-selected row
						// so a subsequent SHIFT+click doesn't re-include the just-removed item.
						let maxIdx = -1;
						document
							.querySelectorAll(".af-file-row.is-selected:not(.af-back-row)")
							.forEach((r) => {
								const idx = parseInt(r.dataset.index, 10);
								if (!isNaN(idx) && idx > maxIdx) maxIdx = idx;
							});
						if (maxIdx !== -1) lastSelectedIndex = maxIdx;
					}
					return;
				}

				// SHIFT+click: range-select from anchor to here.
				// Works even if selection mode isn't active yet.
				if (e.shiftKey) {
					e.preventDefault();
					e.stopPropagation();
					if (lastSelectedIndex === -1) {
						// No anchor yet — select this item and use it as the anchor.
						toggleItem(rowEl, true);
						return;
					}
					const start = Math.min(lastSelectedIndex, currentIndex);
					const end = Math.max(lastSelectedIndex, currentIndex);
					let m = 0;
					const rows = document.querySelectorAll(".af-file-row");
					if (rows.length > 0 && rows[0].classList.contains("af-back-row")) {
						m = 1;
						if (rows.length === 1) return;
					}
					if (!isSelectionMode) enterSelectionMode(table);
					// Always select on SHIFT+click; anchor (lastSelectedIndex) stays fixed.
					for (let i = start + m; i <= end + m; i++) {
						toggleItem(rows[i], true, false);
					}
					updateBatchBar();
					return;
				}

				if (!isSelectionMode) return;

				e.preventDefault();
				e.stopPropagation();
				toggleItem(rowEl);
			};

			// 1. Direct Checkbox Click
			checkbox.onclick = (e) => {
				e.stopPropagation();
				toggleItem(rowEl, checkbox.checked);
			};
		}

		fragment.appendChild(row);
	});

	listBody.appendChild(fragment);

	// Global Drag and Drop to open Upload Dialog
	const dragOverHandler = (e) => {
		e.preventDefault();
		document.body.classList.add("drag-over");
	};
	const dragLeaveHandler = (e) => {
		e.preventDefault();
		document.body.classList.remove("drag-over");
	};
	const dropHandler = (e) => {
		e.preventDefault();
		document.body.classList.remove("drag-over");
		if (e.dataTransfer.files && e.dataTransfer.files.length > 0) {
			openUploadDialog(path, e.dataTransfer.files);
		}
	};

	if (window._afDragOverHandler) {
		document.body.removeEventListener("dragover", window._afDragOverHandler);
		document.body.removeEventListener("dragenter", window._afDragOverHandler);
		document.body.removeEventListener("dragleave", window._afDragLeaveHandler);
		document.body.removeEventListener("drop", window._afDropHandler);
	}
	window._afDragOverHandler = dragOverHandler;
	window._afDragLeaveHandler = dragLeaveHandler;
	window._afDropHandler = dropHandler;

	document.body.addEventListener("dragover", dragOverHandler);
	document.body.addEventListener("dragenter", dragOverHandler);
	document.body.addEventListener("dragleave", dragLeaveHandler);
	document.body.addEventListener("drop", dropHandler);

	return content;
}

function renderBreadcrumbs(path) {
	const segments = path.split("/").filter((s) => s.length > 0);
	const rootEl =
		segments.length === 0
			? `<span class="af-breadcrumb-current">🏠 Root</span>`
			: `<a href="/" class="af-breadcrumb-link nav-link">🏠 Root</a>`;
	let html = `<div class="af-breadcrumb-item">${rootEl}</div>`;

	let cumulativePath = "";
	segments.forEach((segment, index) => {
		cumulativePath += `/${segment}`;
		const displayText = decodeURIComponent(segment);
		const isLast = index === segments.length - 1;
		if (isLast) {
			html += `<div class="af-breadcrumb-item"><span class="af-breadcrumb-current">${displayText}</span></div>`;
		} else {
			html += `
                <div class="af-breadcrumb-item">
                    <a href="${cumulativePath}" class="af-breadcrumb-link nav-link">${displayText}</a>
                </div>`;
		}
	});
	return html;
}
