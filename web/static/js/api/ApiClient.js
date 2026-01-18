const BASE_URL = '/_/api/v1';

/**
 * Helper to handle fetch responses with optional JSON parsing
 */
async function handleResponse(response) {
    // 1. VITAL CHECK: Is this even a Response object?
    // If a network error happens, fetch throws an error before reaching here.
    // If some logic passed an Error object here, we handle it gracefully.
    if (!response || typeof response.text !== 'function') {
        console.error("Invalid response object received:", response);
        throw new Error("Network error or invalid server response");
    }

    // 2. Handle Authentication failures (401) globally
    if (response.status === 401) {
        // Clear local storage
        localStorage.removeItem('af_token');
        localStorage.removeItem('af_user');
        // Notify the app to show the login dialog
        window.dispatchEvent(new CustomEvent('af:require-login'));

        // We still parse the error info if available
        const errData = await response.json().catch(() => ({}));
        throw new Error(errData.error || "Session expired. Please log in.");
    }

    // 3. Handle "No Content" (204) immediately
    if (response.status === 204) return null;

    // 4. Read the body as text first to avoid "Body already read" errors
    const text = await response.text();
    let data = undefined;

    // 5. Attempt to parse as JSON only if content exists and header matches
    const contentType = response.headers.get('Content-Type');
    if (text && contentType && contentType.includes('application/json')) {
        try {
            data = JSON.parse(text);
        } catch (err) {
            console.error("Failed to parse JSON despite header:", err);
        }
    }

    // 6. Handle Success (2xx)
    if (response.ok) {
        return data !== undefined ? data : (text || null);
    }

    // 7. Handle Errors (4xx, 5xx)
    const errorMessage = (data && (data.error || data.message))
        || text
        || `Error ${response.status}: ${response.statusText}`;

    const error = new Error(errorMessage);
    error.status = response.status;
    error.data = data;

    throw error;
}

async function apiFetch(url, options = {}) {
    const token = localStorage.getItem('af_token');
    const headers = {
        ...options.headers
    };

    if (token) {
        headers['Authorization'] = `Bearer ${token}`;
    }

    const res = await fetch(url, { ...options, headers });
    return handleResponse(res);
}

export const API = {

    async login(data) {
        return await apiFetch('/_/api/login', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(data)
        });
    },

    async listFiles(path = '') {
        const url = `${BASE_URL}/fs/${path}`.replace(/\/+/g, '/');
        return apiFetch(url);
    },

    async deleteFile(path) {
        const url = `${path}`.replace(/\/+/g, '/');
        return await apiFetch(url, {
            method: 'DELETE',
        });
    },

    async patchEntry(path, body) {
        const url = `${BASE_URL}/fs/${path}`.replace(/\/+/g, '/');
        return await apiFetch(url, {
            method: 'PATCH',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(body),
        });
    },

    async createDirectory(path) {
        const url = `${BASE_URL}/fs/${path.replace(/\/+/g, '/')}`;
        return await apiFetch(url, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ create_dir: true }),
        });
    },

    async renameResource(oldPath, newName) {
        const url = `${BASE_URL}/fs/${oldPath.replace(/\/+/g, '/')}`;
        return await apiFetch(url, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ rename_to: newName })
        });
    },

    async getStreams() {
        return await apiFetch('/_/api/v1/streams');
    },

    async getStreamGroups(name) {
        return await apiFetch(`/_/api/v1/streams/${name}`);
    },

    async search(query) {
        return await apiFetch(`/_/api/v1/search?q=${encodeURIComponent(query)}`);
    },

    async getSettings() {
        return await apiFetch('/_/api/v1/settings');
    },

    async triggerSync() {
        return await apiFetch('/_/api/v1/system/sync', {
            method: 'POST',
        });
    },

    async getUsers() {
        return await apiFetch('/_/api/admin/users');
    },

    async deleteUser(id) {
        return await apiFetch(`/_/api/admin/users/${id}`, {
            method: 'DELETE',
        });
    },

    async createUser(data) {
        return await apiFetch(`/_/api/admin/users`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(data)
        });
    },

    async updateUser(id, data) {
        return await apiFetch(`/_/api/admin/users/${id}`, {
            method: 'PATCH',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(data)
        });
    },

    async getTokens() {
        return apiFetch('/_/api/admin/tokens');
    },

    async createToken(payload) {
        return apiFetch('/_/api/admin/tokens', {
            method: 'POST',
            body: JSON.stringify(payload)
        });
    },

    async deleteToken(id) {
        return apiFetch(`/_/api/admin/tokens/${id}`, {
            method: 'DELETE'
        });
    }
};
