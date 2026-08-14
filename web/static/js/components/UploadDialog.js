import { API } from "../api/ApiClient.js";
import { Format } from "../api/Format.js";
import { TransferManager } from "../api/TransferManager.js";
import { showToast } from "./Toast.js";

const template = document.createElement("template");
template.innerHTML = `
    <dialog class="af-modal" id="upload-dialog" style="max-width: 800px;">
        <form class="af-form">
            <div class="af-modal-header">
                <h3>Upload</h3>
                <button type="button" class="btn btn-ghost modal-close">×</button>
            </div>
            
            <div class="af-modal-body">
                <div class="af-upload-grid">
                    <!-- Left: Metadata -->
                    <div class="af-upload-meta" style="display: flex; flex-direction: column; gap: 10px;">
                        
                        <label style="display: flex; flex-direction: column; gap: 4px;">
                            <span>Stream</span>
                            <div style="display: flex; flex-direction: column; gap: 4px;">
                                <input type="text" name="stream_name" id="upload-stream-name" placeholder="e.g. project-name" style="width: 100%; padding: 10px; border: 1px solid var(--border); border-radius: var(--radius); font-size: 14px; box-sizing: border-box;">
                                <button type="button" class="btn btn-ghost" id="upload-stream-configure-btn" style="display: none; align-self: flex-start; font-size: 11px; padding: 4px 8px; min-height: 24px; height: 24px; margin-top: 2px;">⚙️ Update Stream</button>
                            </div>
                        </label>
                        
                        <label style="display: flex; flex-direction: column; gap: 4px;">
                            <span>Group Name</span>
                            <input type="text" name="group_name" id="upload-group-name" placeholder="e.g. v1.0" style="width: 100%; padding: 10px; border: 1px solid var(--border); border-radius: var(--radius); font-size: 14px; box-sizing: border-box;">
                        </label>
                        <div id="upload-stream-validation" class="af-alert af-alert-error" style="display: none; padding: 10px; margin-top: -6px;">
                            <strong>⚠️ Required:</strong> Both Stream Name and Group Name must be provided.
                        </div>
                        
                        <label style="display: flex; flex-direction: column; gap: 4px;">
                            <span>Tags</span>
                            <input type="text" name="tags" placeholder="env=prod, arch=x64" style="padding: 10px; border: 1px solid var(--border); border-radius: var(--radius); font-size: 14px;">
                        </label>
                        
                        <label style="display: flex; flex-direction: column; gap: 4px;">
                            <span>Expiration Policy</span>
                            <select name="expiry_type" id="upload-expiry-type" style="padding: 10px; border: 1px solid var(--border); border-radius: var(--radius); font-size: 14px; background: var(--bg-main);">
                                <option value="none">No Expiration</option>
                                <option value="after_upload">Expire After Upload (Relative)</option>
                                <option value="after_download">Expire After Download (Sliding)</option>
                                <option value="absolute">Specific Date/Time (Absolute)</option>
                            </select>
                        </label>
                        
                        <div id="expiry-input-wrapper" style="display: none;">
                            <div id="expiry-duration-wrapper" style="display: none; flex-direction: column; gap: 4px;">
                                <label style="display: flex; flex-direction: column; gap: 4px; margin: 0;">
                                    <span>Duration</span>
                                    <input type="text" name="expiry_duration" id="upload-expiry-duration" placeholder="e.g. 7d or 24h" style="padding: 10px; border: 1px solid var(--border); border-radius: var(--radius); font-size: 14px;">
                                </label>
                                <small class="af-input-help" style="font-size: 11px; color: var(--text-muted);">Use durations like 7d, 2h, 30m.</small>
                            </div>
                            
                            <div id="expiry-absolute-wrapper" style="display: none; flex-direction: column; gap: 4px;">
                                <label style="display: flex; flex-direction: column; gap: 4px; margin: 0;">
                                    <span>Expiration Date</span>
                                    <div class="af-input-with-action">
                                        <input type="text" name="expiry_absolute" id="upload-expiry-absolute" placeholder="YYYY-MM-DD HH:MM:SS" style="padding: 10px; padding-right: 40px; border: 1px solid var(--border); border-radius: var(--radius); font-size: 14px; width: 100%;">
                                        <button type="button" class="af-input-icon-btn" id="upload-expiry-picker-trigger" title="Open Calendar" style="position: absolute; right: 10px; top: 50%; transform: translateY(-50%); background: none; border: none; cursor: pointer; font-size: 16px;">📅</button>
                                        <input type="date" id="upload-expiry-hidden-picker" style="opacity: 0; position: absolute; left: 0; top: 0; width: 100%; height: 100%; pointer-events: none;">
                                    </div>
                                </label>
                                <small class="af-input-help" style="font-size: 11px; color: var(--text-muted);">YYYY-MM-DD HH:MM:SS format or use the calendar button.</small>
                            </div>
                        </div>
                    </div>

                    <!-- Right: File Staging -->
                    <div class="af-upload-staging" style="display: flex; flex-direction: column; gap: 10px; height: 100%; min-height: 0;">
                        
                        <div id="upload-dropzone" style="border: 2px dashed var(--border); border-radius: var(--radius); padding: 20px; text-align: center; background: var(--bg-alt); cursor: pointer; transition: background 0.2s;">
                            <span style="font-size: 24px; display: block; margin-bottom: 8px;">📁</span>
                            <div style="font-size: 13px; color: var(--text-muted);">
                                <strong>Click to browse</strong> or drag and drop files here
                            </div>
                            <input type="file" id="file-input" multiple style="display:none">
                        </div>
                        
                        <div class="af-table-wrapper" style="flex: 1; min-height: 150px; overflow-y:auto; margin: 0;">
                            <table class="af-table af-table-compact">
                                <thead>
                                    <tr>
                                        <th>Name</th>
                                        <th style="width:70px">Size</th>
                                        <th style="width:40px"></th>
                                    </tr>
                                </thead>
                                <tbody id="staging-body">
                                    <!-- Files injected here -->
                                </tbody>
                            </table>
                        </div>
                    </div>
                </div>
            </div>

            <div class="af-modal-footer">
                <button type="button" class="btn btn-ghost modal-close">Cancel</button>
                <button type="submit" class="btn btn-primary" id="start-upload-btn" disabled>
                    Start Upload (0 files)
                </button>
            </div>
        </form>
    </dialog>
`;

const streamSettingsTemplate = document.createElement("template");
streamSettingsTemplate.innerHTML = `
    <dialog class="af-modal" id="upload-stream-settings-dialog" style="max-width: 400px;">
        <form class="af-form">
            <div class="af-modal-header">
                <h3>Stream Settings</h3>
                <button type="button" class="btn btn-ghost modal-close">×</button>
            </div>
            <div class="af-modal-body">
                <div style="margin-bottom: 15px;">
                    <label class="af-check-group">
                        <input type="checkbox" name="stream_retain_latest" id="upload-stream-retain-latest"> 
                        <span>Retain Latest Group</span>
                    </label>
                    <small class="af-input-help" style="margin-left: 28px; display: block;">The latest group's expiry is paused/inactive, keeping it alive.</small>
                </div>
                <div style="margin-bottom: 15px;">
                    <label style="display: flex; flex-direction: column; gap: 4px;">
                        <span>Latest Group Max Expiry</span>
                        <input type="text" name="stream_retain_latest_max_expiry" id="upload-stream-retain-latest-max-expiry" placeholder="e.g. 90d" style="padding: 10px; border: 1px solid var(--border); border-radius: var(--radius); font-size: 14px;">
                    </label>
                    <small class="af-input-help" style="display: block; margin-top: 4px;">If "Retain Latest" is on, this forces the latest group to eventually expire.</small>
                </div>
                <div style="margin-bottom: 15px;">
                    <label class="af-check-group">
                        <input type="checkbox" name="stream_auto_expire_previous" id="upload-stream-auto-expire-previous"> 
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

let stagingList = [];
let path;

export function openUploadDialog(currentPath, initialFiles = null) {
	if (!document.getElementById("upload-dialog")) {
		document.body.appendChild(template.content.cloneNode(true));
		document.body.appendChild(streamSettingsTemplate.content.cloneNode(true));
		setupListeners();
	}

	const dialog = document.getElementById("upload-dialog");
	const form = dialog.querySelector("form");
	path = currentPath;

	if (dialog.hasAttribute("open")) {
		if (initialFiles && initialFiles.length > 0) {
			stagingList = [...stagingList, ...Array.from(initialFiles)];
		}
		renderStaging(dialog);
		return;
	}

	// Clear state on open
	stagingList = [];
	if (initialFiles && initialFiles.length > 0) {
		stagingList = [...Array.from(initialFiles)];
	}
	renderStaging(dialog);

	// Reset form inputs
	form.reset();
	dialog.querySelector("#upload-stream-configure-btn").style.display = "none";
	dialog.querySelector("#expiry-input-wrapper").style.display = "none";
	dialog.querySelector("#expiry-duration-wrapper").style.display = "none";
	dialog.querySelector("#expiry-absolute-wrapper").style.display = "none";

	dialog.showModal();
}

function renderStaging(dialog) {
	const tbody = dialog.querySelector("#staging-body");
	const submitBtn = dialog.querySelector("#start-upload-btn");

	tbody.innerHTML = "";

	stagingList.forEach((file, index) => {
		const row = document.createElement("tr");
		row.innerHTML = `
            <td class="af-file-text" style="max-width: 200px; overflow:hidden; text-overflow:ellipsis; white-space:nowrap;" title="${Format.escapeHtml(file.name)}">
                ${Format.escapeHtml(file.name)}
            </td>
            <td class="af-col-mono" style="font-size: 11px;">${Format.formatBytes(file.size)}</td>
            <td>
                <button type="button" class="btn btn-ghost btn-danger remove-file-btn" data-index="${index}">×</button>
            </td>
        `;
		tbody.appendChild(row);
	});

	if (stagingList.length === 0) {
		tbody.innerHTML = `<tr><td colspan="3" class="af-text-muted" style="text-align:center; padding: 20px;">No files added</td></tr>`;
	}

	submitBtn.disabled = stagingList.length === 0;
	submitBtn.textContent = `Start Upload (${stagingList.length} files)`;

	// Attach remove events
	tbody.querySelectorAll(".remove-file-btn").forEach((btn) => {
		btn.onclick = () => {
			stagingList.splice(parseInt(btn.dataset.index, 10), 1);
			renderStaging(dialog);
		};
	});
}

function setupListeners() {
	const dialog = document.getElementById("upload-dialog");
	const form = dialog.querySelector("form");
	const fileInput = dialog.querySelector("#file-input");
	const dropzone = dialog.querySelector("#upload-dropzone");

	const addFiles = (files) => {
		stagingList = [...stagingList, ...Array.from(files)];
		renderStaging(dialog);
	};

	if (dropzone) {
		dropzone.onclick = () => fileInput.click();
	}
	fileInput.onchange = () => addFiles(fileInput.files);

	dialog.querySelectorAll(".modal-close").forEach((btn) => {
		btn.onclick = () => dialog.close();
	});

	// Expiry Policy Picker UI logic
	const expiryTypeSelect = dialog.querySelector("#upload-expiry-type");
	const expiryInputWrapper = dialog.querySelector("#expiry-input-wrapper");
	const expiryDurationWrapper = dialog.querySelector(
		"#expiry-duration-wrapper",
	);
	const expiryAbsoluteWrapper = dialog.querySelector(
		"#expiry-absolute-wrapper",
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

	// Calendar picker listener for absolute expiry
	const absoluteInput = dialog.querySelector("#upload-expiry-absolute");
	const hiddenPicker = dialog.querySelector("#upload-expiry-hidden-picker");
	const trigger = dialog.querySelector("#upload-expiry-picker-trigger");

	hiddenPicker.addEventListener("input", () => {
		if (hiddenPicker.value) {
			const selectedDate = hiddenPicker.value;
			const defaultTime = "23:59:59";
			absoluteInput.value = `${selectedDate} ${defaultTime}`;
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

	let debounceTimer;
	const streamNameInput = dialog.querySelector("#upload-stream-name");
	const configureBtn = dialog.querySelector("#upload-stream-configure-btn");
	const settingsDialog = document.getElementById(
		"upload-stream-settings-dialog",
	);

	// Keep settings in memory
	const currentStreamSettings = {
		retain_latest: false,
		retain_latest_max_expiry: "",
		auto_expire_previous: false,
	};

	streamNameInput.addEventListener("input", () => {
		dialog.querySelector("#upload-stream-validation").style.display = "none";
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
		}, 500);
	});

	const groupNameInput = dialog.querySelector("#upload-group-name");
	if (groupNameInput) {
		groupNameInput.addEventListener("input", () => {
			dialog.querySelector("#upload-stream-validation").style.display = "none";
		});
	}

	dialog.querySelector("#upload-stream-configure-btn").onclick = () => {
		settingsDialog.querySelector("#upload-stream-retain-latest").checked =
			currentStreamSettings.retain_latest;
		settingsDialog.querySelector(
			"#upload-stream-retain-latest-max-expiry",
		).value = currentStreamSettings.retain_latest_max_expiry;
		settingsDialog.querySelector(
			"#upload-stream-auto-expire-previous",
		).checked = currentStreamSettings.auto_expire_previous;
		settingsDialog.showModal();
	};

	settingsDialog.querySelector(".af-form").onsubmit = async (e) => {
		e.preventDefault();
		const streamName = streamNameInput.value.trim();

		currentStreamSettings.retain_latest = settingsDialog.querySelector(
			"#upload-stream-retain-latest",
		).checked;
		currentStreamSettings.retain_latest_max_expiry = settingsDialog
			.querySelector("#upload-stream-retain-latest-max-expiry")
			.value.trim();
		currentStreamSettings.auto_expire_previous = settingsDialog.querySelector(
			"#upload-stream-auto-expire-previous",
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

	settingsDialog.querySelectorAll(".modal-close").forEach((btn) => {
		btn.onclick = () => settingsDialog.close();
	});

	form.onsubmit = async (e) => {
		e.preventDefault();

		const fd = new FormData(form);
		const streamName = fd.get("stream_name")?.trim();
		const groupName = fd.get("group_name")?.trim();
		const validationMsg = dialog.querySelector("#upload-stream-validation");

		if ((streamName && !groupName) || (!streamName && groupName)) {
			validationMsg.style.display = "block";
			return;
		}
		validationMsg.style.display = "none";

		const submitBtn = dialog.querySelector("#start-upload-btn");
		submitBtn.disabled = true;

		const headers = {
			"X-Tags": fd.get("tags"),
		};

		if (streamName && groupName) {
			headers["Yaar-Stream"] = streamName;
			headers["Yaar-Group"] = groupName;
		}

		const expiryType = fd.get("expiry_type");
		if (expiryType === "after_upload") {
			const val = fd.get("expiry_duration")?.trim();
			if (val) headers["Yaar-Retention-Expire-After-Upload"] = val;
		} else if (expiryType === "after_download") {
			const val = fd.get("expiry_duration")?.trim();
			if (val) headers["Yaar-Retention-Expire-After-Download"] = val;
		} else if (expiryType === "absolute") {
			const val = fd.get("expiry_absolute")?.trim();
			if (val) {
				const date = new Date(val.replace(" ", "T"));
				if (!Number.isNaN(date.getTime()) && date < new Date()) {
					showToast("Expiry date cannot be in the past.", "error");
					submitBtn.disabled = false;
					return;
				}
				headers["Yaar-Retention-Expire-At"] = val;
			}
		}

		stagingList.forEach((file) => {
			const uploadUrl = `/${path}`.replace(/\/+/g, "/");
			TransferManager.upload(file, uploadUrl, path, headers);
		});

		dialog.close();
	};
}
