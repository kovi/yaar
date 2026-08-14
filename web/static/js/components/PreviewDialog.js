import { Format } from "../api/Format.js";

const MAX_TEXT_PREVIEW_SIZE = 2 * 1024 * 1024; // 2MB

export function isPreviewable(file) {
	if (file.isdir) return false;
	const ext = file.name.split(".").pop().toLowerCase();
	const previewableExts = new Set([
		// Images
		"png",
		"jpg",
		"jpeg",
		"gif",
		"webp",
		"svg",
		"bmp",
		"ico",
		// Video
		"mp4",
		"webm",
		"ogg",
		"mov",
		// Audio
		"mp3",
		"wav",
		"flac",
		// PDF
		"pdf",
		// Text/Code
		"txt",
		"md",
		"json",
		"yaml",
		"yml",
		"js",
		"ts",
		"html",
		"css",
		"go",
		"py",
		"sh",
		"bash",
		"log",
		"csv",
		"xml",
		"ini",
		"conf",
	]);
	return previewableExts.has(ext);
}

export function openPreviewDialog(file, fullPath) {
	const dialog = document.createElement("dialog");
	dialog.className = "af-modal af-modal-preview";
	dialog.style.zIndex = "2000";

	const fileUrl = `/${fullPath}`.replace(/\/+/g, "/");

	dialog.innerHTML = `
        <div class="af-modal-header" style="border-bottom: 1px solid var(--border); padding: 15px;">
            <div style="display: flex; flex-direction: column;">
                <h3 style="margin: 0; font-size: 16px; word-break: break-all;">${Format.escapeHtml(file.name)}</h3>
                <span class="af-text-muted" style="font-size: 11px;">${Format.escapeHtml(Format.formatBytes(file.size))}</span>
            </div>
            <div style="display: flex; gap: 10px; align-items: center;">
                <a href="${Format.escapeHtml(fileUrl)}" download class="btn btn-primary btn-sm" target="_blank">Download</a>
                <button type="button" class="btn btn-ghost modal-close" style="font-size: 20px;">×</button>
            </div>
        </div>
        <div class="af-modal-body" id="preview-container">
            <div class="af-loading-spinner" style="margin: 40px auto; display: block; width: 40px; height: 40px; border: 4px solid var(--border); border-top-color: var(--primary); border-radius: 50%; animation: spin 1s linear infinite;"></div>
        </div>
    `;

	document.body.appendChild(dialog);
	dialog.showModal();

	const container = dialog.querySelector("#preview-container");

	dialog.addEventListener("close", () => {
		dialog.remove();
	});

	dialog.querySelectorAll(".modal-close").forEach((btn) => {
		btn.onclick = () => {
			dialog.close();
		};
	});

	renderPreviewContent(file, fileUrl, container);
}

async function renderPreviewContent(file, fileUrl, container) {
	const ext = file.name.split(".").pop().toLowerCase();

	const imageExts = new Set([
		"png",
		"jpg",
		"jpeg",
		"gif",
		"webp",
		"svg",
		"bmp",
		"ico",
	]);
	const videoExts = new Set(["mp4", "webm", "ogg", "mov"]);
	const audioExts = new Set(["mp3", "wav", "flac"]);

	// fileUrl embeds the filename, which may contain a quote and would otherwise
	// break out of the src attribute below.
	const safeUrl = Format.escapeHtml(fileUrl);

	if (imageExts.has(ext)) {
		container.innerHTML = `<img src="${safeUrl}" style="max-width: 100%; max-height: 100%; object-fit: contain;">`;
		return;
	}

	if (videoExts.has(ext)) {
		container.innerHTML = `<video controls src="${safeUrl}" style="max-width: 100%; max-height: 100%;"></video>`;
		return;
	}

	if (audioExts.has(ext)) {
		container.innerHTML = `<audio controls src="${safeUrl}" style="width: 100%; max-width: 500px; margin: auto;"></audio>`;
		return;
	}

	if (ext === "pdf") {
		container.innerHTML = `<iframe src="${safeUrl}" style="width: 100%; height: 100%; border: none;"></iframe>`;
		return;
	}

	// Fallback to text/code
	if (file.size > MAX_TEXT_PREVIEW_SIZE) {
		container.innerHTML = `
            <div style="color: #fff; text-align: center; margin: auto;">
                <p>This file is too large to preview inline (${Format.formatBytes(file.size)}).</p>
                <p class="af-text-muted">Maximum preview size is ${Format.formatBytes(MAX_TEXT_PREVIEW_SIZE)}.</p>
            </div>
        `;
		return;
	}

	try {
		const res = await fetch(fileUrl);
		if (!res.ok) throw new Error(`HTTP ${res.status}`);
		const text = await res.text();

		// Escape HTML to prevent XSS
		const escapedText = text
			.replace(/&/g, "&amp;")
			.replace(/</g, "&lt;")
			.replace(/>/g, "&gt;")
			.replace(/"/g, "&quot;")
			.replace(/'/g, "&#039;");

		container.innerHTML = `
            <pre style="width: 100%; height: 100%; overflow: auto; margin: 0; padding: 20px; box-sizing: border-box; background: #1e1e1e; color: #d4d4d4; font-family: monospace; font-size: 13px; line-height: 1.5; white-space: pre-wrap;"><code style="font-family: inherit;">${escapedText}</code></pre>
        `;
	} catch (err) {
		container.innerHTML = `
            <div style="color: var(--danger); text-align: center; margin: auto;">
                <p>Failed to load file content for preview.</p>
                <p class="af-text-muted">${Format.escapeHtml(err.message)}</p>
            </div>
        `;
	}
}
