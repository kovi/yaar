import { API } from '../api/ApiClient.js';

let debounceTimer;

export function initGlobalSearch() {
    const container = document.querySelector('.af-navbar-search');
    const input = container.querySelector('#global-search');

    // Create dropdown element
    const dropdown = document.createElement('div');
    dropdown.className = 'af-search-dropdown hidden';
    container.appendChild(dropdown);

    input.addEventListener('input', (e) => {
        clearTimeout(debounceTimer);
        const q = e.target.value.trim();

        if (q.length < 2) {
            dropdown.classList.add('hidden');
            return;
        }

        debounceTimer = setTimeout(async () => {
            const results = await API.search(q);
            renderResults(results, dropdown);
        }, 300);
    });

    // Close dropdown when clicking outside
    document.addEventListener('click', (e) => {
        if (!container.contains(e.target)) dropdown.classList.add('hidden');
    });

    window.addEventListener('keydown', (e) => {
        if (e.key === '/' && document.activeElement.tagName !== 'INPUT') {
            e.preventDefault();
            document.getElementById('global-search').focus();
        }
    });
}

function renderResults(results, dropdown) {
    if (!results || results.length === 0) {
        dropdown.innerHTML = `<div class="af-search-empty">No results found</div>`;
    } else {
        dropdown.innerHTML = results.map(r => `
            <div class="af-search-item" data-path="${r.path}" data-type="${r.type}">
                <div class="af-search-icon">${getIcon(r.type)}</div>
                <div class="af-search-content">
                    <div class="af-search-top">
                        <span class="af-search-name">${r.name}</span>
                        <span class="af-search-path">${r.path}</span>
                    </div>
                    <div class="af-search-meta">
                        ${r.stream && r.group ? `<span class="badge-origin badge-stream">${r.stream}/${r.group}</span>` : ''}
                        ${(r.tags || []).map(t => `<span class="badge-tag">${t}</span>`).join('')}
                    </div>
                </div>
            </div>
        `).join('');

        dropdown.querySelectorAll('.af-search-item').forEach(item => {
            item.onclick = () => {
                const fullPath = item.dataset.path;
                const lastSlash = fullPath.lastIndexOf('/');
                const parentPath = lastSlash <= 0 ? '/' : fullPath.substring(0, lastSlash);
                const fileName = fullPath.substring(lastSlash + 1);

                let path;
                let search = "";
                if (item.dataset.type == "file") {
                    path = parentPath;
                    search = `?highlight=${fileName}`
                } else {
                    path = fullPath;
                }

                // Navigate to the file or directory
                console.log("navigate to", path, item.dataset)
                window.history.pushState(null, '', path + search);
                window.dispatchEvent(new CustomEvent('artifactory:navigated'));

                // Clear and close
                dropdown.classList.add('hidden');
                document.getElementById('global-search').value = "";
            };
        });
    }
    dropdown.classList.remove('hidden');
}

function getIcon(type) {
    if (type === 'dir') return '📁';
    if (type === 'stream') return '📡';
    return '📄';
}