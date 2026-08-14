/**
 * Highly compatible copy-to-clipboard that works in Modals and HTTP.
 */
export async function copyToClipboard(text) {
	// 1. Try Modern API first
	if (navigator.clipboard && window.isSecureContext) {
		try {
			await navigator.clipboard.writeText(text);
			return true;
		} catch (_err) {}
	}

	// 2. Fallback for HTTP / Modals
	const textArea = document.createElement("textarea");
	textArea.value = text;

	// Prevent scrolling to bottom when focused
	textArea.style.position = "fixed";
	textArea.style.left = "-9999px";
	textArea.style.top = "0";
	textArea.style.opacity = "0";

	// IMPORTANT: If a modal is open, append to the modal, not the body.
	// This bypasses the focus-trap of the <dialog> element.
	const openModals = document.querySelectorAll("dialog[open]");
	const container =
		openModals.length > 0
			? openModals[openModals.length - 1] // Get the very last one opened
			: document.body;
	container.appendChild(textArea);

	// Select the text
	textArea.focus();
	textArea.select();

	// Mobile/iOS specific selection
	textArea.setSelectionRange(0, 99999);

	let success = false;
	try {
		success = document.execCommand("copy");
	} catch (_err) {
		success = false;
	}

	container.removeChild(textArea);
	return success;
}
