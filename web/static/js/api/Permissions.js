import { Auth } from "./Auth.js";

export const Permissions = {
	/**
	 * Checks if the current user can perform an action at a specific path
	 */
	canWrite(targetPath) {
		const user = Auth.getUser();
		if (!user) return false;
		if (user.isAdmin) return true; // Admins can do anything

		const scopes = user.allowedPaths || [];
		if (scopes.length === 0) return false;
		if (scopes.includes("/")) return true;

		// Normalize path
		const req =
			"/" +
			targetPath
				.split("/")
				.filter((p) => p)
				.join("/");

		// Check if any allowed scope is a parent of the target path
		return scopes.some((scope) => {
			const cleanScope =
				"/" +
				scope
					.split("/")
					.filter((p) => p)
					.join("/");
			return req === cleanScope || req.startsWith(`${cleanScope}/`);
		});
	},

	/**
	 * Scans the document and hides elements based on their data-attributes
	 */
	applyGlobalRules(currentPath) {
		// Find all elements marked for protection
		const elements = document.querySelectorAll("[data-af-perms]");
		const hasWriteAccess = this.canWrite(currentPath);

		elements.forEach((el) => {
			const rule = el.dataset.afPerms;

			if (rule === "write") {
				// Toggle visibility based on write access
				el.classList.toggle("af-hidden-perms", !hasWriteAccess);
			}
		});
	},
};
