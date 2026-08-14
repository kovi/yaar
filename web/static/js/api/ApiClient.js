const BASE_URL = "/_/api/v1";

/**
 * Helper to handle fetch responses with optional JSON parsing
 */
async function handleResponse(response) {
	// 1. VITAL CHECK: Is this even a Response object?
	// If a network error happens, fetch throws an error before reaching here.
	// If some logic passed an Error object here, we handle it gracefully.
	if (!response || typeof response.text !== "function") {
		console.error("Invalid response object received:", response);
		throw new Error("Network error or invalid server response");
	}

	// 2. Handle Authentication failures (401) globally
	if (response.status === 401) {
		const isLoginEndpoint = response.url.includes("/login");
		const hadToken = !!localStorage.getItem("af_token");
		let sessionExpired = false;

		if (!isLoginEndpoint && hadToken) {
			// This is a session expiry/revocation
			localStorage.removeItem("af_token");
			localStorage.removeItem("af_user");
			window.dispatchEvent(new CustomEvent("af:session-expired"));
			sessionExpired = true;
		}

		// We still parse the error info if available
		const errData = await response.json().catch(() => ({}));
		const err = new Error(errData.error || "Unauthorized");
		err.status = 401;
		err.sessionExpired = sessionExpired;
		throw err;
	}

	if (response.status === 403) {
		// Logged in, but restricted by Path Scope or Admin flag
		const errData = await response.json().catch(() => ({}));
		const err = new Error(
			errData.error || "Access Denied: You do not have permission for this.",
		);
		err.status = 403;
		throw err;
	}

	// 3. Handle "No Content" (204) immediately
	if (response.status === 204) return null;

	// 4. Read the body as text first to avoid "Body already read" errors
	const text = await response.text();
	let data;

	// 5. Attempt to parse as JSON only if content exists and header matches
	const contentType = response.headers.get("Content-Type");
	if (text && contentType?.includes("application/json")) {
		try {
			data = JSON.parse(text);
		} catch (err) {
			console.error("Failed to parse JSON despite header:", err);
		}
	}

	// 6. Handle Success (2xx)
	if (response.ok) {
		return data !== undefined ? data : text || null;
	}

	// 7. Handle Errors (4xx, 5xx)
	const errorMessage =
		(data && (data.error || data.message)) ||
		text ||
		`Error ${response.status}: ${response.statusText}`;

	const error = new Error(errorMessage);
	error.status = response.status;
	error.data = data;

	throw error;
}

// apiFetchWithResponse is apiFetch plus the raw Response, for the few callers
// that need a header (e.g. X-Total-Count on paged listings). Error handling is
// identical — handleResponse still throws on a non-2xx.
async function apiFetchWithResponse(url, options = {}) {
	const token = localStorage.getItem("af_token");
	const headers = {
		...options.headers,
	};

	if (token) {
		headers.Authorization = `Bearer ${token}`;
	}

	const response = await fetch(url, { ...options, headers });
	const data = await handleResponse(response);
	return { data, response };
}

async function apiFetch(url, options = {}) {
	const { data } = await apiFetchWithResponse(url, options);
	return data;
}

export const API = {
	async login(data) {
		return await apiFetch("/_/api/login", {
			method: "POST",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify(data),
		});
	},

	// listFiles returns the *entire* directory, transparently walking the
	// server's pages.
	//
	// The listing endpoint caps each response (X-Total-Count reports the real
	// directory size) so one request can never be asked to materialize a 50k-entry
	// directory. Callers here sort and filter across the whole listing, though, so
	// handing them a single page would silently mis-sort by size or modtime —
	// hence the walk. A non-array response (i.e. "not a directory") is passed
	// straight through for the caller to detect.
	async listFiles(path = "") {
		const base = `${BASE_URL}/fs/${path}`.replace(/\/+/g, "/");
		const pageSize = 1000; // matches the server's maxListLimit

		const all = [];
		let offset = 0;

		while (true) {
			const sep = base.includes("?") ? "&" : "?";
			const { data, response } = await apiFetchWithResponse(
				`${base}${sep}limit=${pageSize}&offset=${offset}`,
			);

			if (!Array.isArray(data)) {
				// Not a directory — hand the raw payload back unchanged.
				return offset === 0 ? data : all;
			}

			all.push(...data);

			const total = Number(response.headers.get("X-Total-Count"));
			offset += data.length;

			// Stop on a short page, once we have everything, or if the server
			// returned nothing (guards against a loop if limit were ignored).
			if (data.length === 0 || data.length < pageSize) break;
			if (Number.isFinite(total) && total > 0 && offset >= total) break;
		}

		all.forEach((f) => {
			f.isdir = f.type === "dir";
		});
		return all;
	},

	async deleteFile(path) {
		const url = `${path}`.replace(/\/+/g, "/");
		return await apiFetch(url, {
			method: "DELETE",
		});
	},

	async batchDelete(paths) {
		return await apiFetch(`${BASE_URL}/batch`, {
			method: "DELETE",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify({ paths }),
		});
	},

	async patchEntry(path, body) {
		const url = `${BASE_URL}/fs/${path}`.replace(/\/+/g, "/");
		return await apiFetch(url, {
			method: "PATCH",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify(body),
		});
	},

	async createDirectory(path) {
		const url = `${BASE_URL}/fs/${path.replace(/\/+/g, "/")}`;
		return await apiFetch(url, {
			method: "POST",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify({ create_dir: true }),
		});
	},

	async renameResource(oldPath, newName) {
		const url = `${BASE_URL}/fs/${oldPath.replace(/\/+/g, "/")}`;
		return await apiFetch(url, {
			method: "POST",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify({ rename_to: newName }),
		});
	},

	async moveResource(path, moveTo) {
		const url = `${BASE_URL}/fs/${path.replace(/\/+/g, "/")}`;
		return await apiFetch(url, {
			method: "POST",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify({ move_to: moveTo }),
		});
	},

	async getStreams() {
		return await apiFetch("/_/api/v1/streams");
	},

	async getStreamGroups(name) {
		const res = await apiFetch(`/_/api/v1/streams/${encodeURIComponent(name)}`);
		if (res && Array.isArray(res.groups)) {
			res.groups.forEach((group) => {
				if (Array.isArray(group.files)) {
					group.files.forEach((f) => {
						f.isdir = f.type === "dir";
					});
				}
			});
		}
		return res;
	},

	async saveStream(name, data) {
		return await apiFetch(`/_/api/v1/streams/${encodeURIComponent(name)}`, {
			method: "PUT",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify(data),
		});
	},

	async search(query) {
		return await apiFetch(`/_/api/v1/search?q=${encodeURIComponent(query)}`);
	},

	async getSettings() {
		return await apiFetch("/_/api/v1/settings");
	},

	async triggerSync() {
		return await apiFetch("/_/api/system/sync", {
			method: "POST",
		});
	},

	async getUsers() {
		return await apiFetch("/_/api/admin/users");
	},

	async deleteUser(id) {
		return await apiFetch(`/_/api/admin/users/${id}`, {
			method: "DELETE",
		});
	},

	async createUser(data) {
		return await apiFetch(`/_/api/admin/users`, {
			method: "POST",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify(data),
		});
	},

	async updateUser(id, data) {
		return await apiFetch(`/_/api/admin/users/${id}`, {
			method: "PATCH",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify(data),
		});
	},

	async getTokens() {
		return apiFetch("/_/api/tokens");
	},

	async createToken(payload) {
		return apiFetch("/_/api/tokens", {
			method: "POST",
			body: JSON.stringify(payload),
		});
	},

	async deleteToken(id) {
		return apiFetch(`/_/api/tokens/${id}`, {
			method: "DELETE",
		});
	},

	async vacuumDatabase() {
		return apiFetch("/_/api/system/vacuum", { method: "POST" });
	},

	async getJanitorErrors() {
		return apiFetch("/_/api/system/janitor/errors");
	},

	async clearJanitorError(key) {
		return apiFetch(`/_/api/system/janitor/errors/${encodeURIComponent(key)}`, {
			method: "DELETE",
		});
	},

	async getAuditLog({
		beforeOffset = -1,
		generation = 0,
		limit = 75,
		filter = "",
	} = {}) {
		const params = new URLSearchParams({ limit });
		if (beforeOffset >= 0) params.set("before_offset", beforeOffset);
		if (generation > 0) params.set("generation", generation);
		if (filter) params.set("filter", filter);
		return apiFetch(`${BASE_URL}/admin/audit-log?${params}`);
	},
};
