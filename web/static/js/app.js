import { Auth } from './api/Auth.js';
import { FileBrowser } from './components/FileBrowser.js';
import { initGlobalSearch } from './components/SearchAutocomplete.js';
import { openLogin } from './components/Login.js';
import { openSettings } from './components/Settings.js';
import { StreamManager } from './components/StreamManager.js';
import { TransferManager } from './api/TransferManager.js';
import { NotFoundView } from './components/NotFoundView.js';


const app = document.getElementById('app');

function renderUserMenu() {
    const container = document.querySelector('.af-navbar-right');
    const user = Auth.getUser();

    if (!Auth.isLoggedIn()) {
        container.innerHTML = `
            <div class="af-status-indicator af-status-guest" id="nav-login-btn">
                Guest
            </div>
        `;
        container.querySelector('#nav-login-btn').onclick = openLogin;
        return;
    }

    // --- AUTHENTICATED STATE ---
    const adminClass = user.isAdmin ? 'af-status-admin' : '';

    container.innerHTML = `
        <div class="af-user-menu-container">
            <div class="af-status-indicator af-status-user ${adminClass}" id="user-menu-trigger">
                <span>${user.username}</span>
                ${user.isAdmin ? '<span class="af-role-label">(admin)</span>' : ''}
            </div>
            <div class="af-dropdown hidden" id="user-dropdown">
                <button class="af-dropdown-item" id="nav-settings-btn">⚙️ Settings</button>
                <div class="af-dropdown-divider"></div>
                <button class="af-dropdown-item" style="color: var(--danger)" id="nav-logout-btn">🚪 Logout</button>
            </div>
        </div>
    `;

    const trigger = container.querySelector('#user-menu-trigger');
    const dropdown = container.querySelector('#user-dropdown');

    trigger.onclick = (e) => {
        e.stopPropagation();
        dropdown.classList.toggle('hidden');
    };

    container.querySelector('#nav-settings-btn').onclick = openSettings;
    container.querySelector('#nav-logout-btn').onclick = () => Auth.logout();

    // Close dropdown when clicking outside
    document.addEventListener('click', () => dropdown.classList.add('hidden'), { once: true });
}

function updateActiveNavLink(activeRoute) {
    const navLinks = document.querySelectorAll('.af-navbar-link[data-route]');
    navLinks.forEach(link => {
        // 3. Toggle the 'active' class based on the match
        if (link.dataset.route === activeRoute) {
            link.classList.add('active');
        } else {
            link.classList.remove('active');
        }
    });
}

/**
* Router Logic: Decides which component to render based on the URL
*/
async function router() {
    const path = window.location.pathname;

    renderUserMenu();

    if (path.startsWith('/_/streams')) {
        updateActiveNavLink("streams");
        const view = await StreamManager(path);
        renderView(view);
        return;
    }

    // 2. Default: File Browser
    try {
        updateActiveNavLink("files");
        const view = await FileBrowser(path);
        renderView(view);
    } catch (err) {
        if (err.status === 404) {
            renderView(NotFoundView(path));
        } else {
            // General error (e.g. 500)
            app.innerHTML = `<div class="af-alert af-alert-error"><strong>Error:</strong> ${err.message}</div>`;
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
window.addEventListener('popstate', () => {
    router()
});
window.addEventListener('pageshow', (event) => {
    // If event.persisted is true, the page was restored from bfcache
    if (event.persisted) {
        console.log("Restored from bfcache, forcing router update...");
        router();
    }
});

document.body.addEventListener('click', e => {
    // 1. Find the nearest parent (or self) that has the .nav-link class
    const link = e.target.closest('.nav-link');

    // 2. If we didn't click a .nav-link, or it's a right-click/ctrl-click, ignore
    if (!link) return;

    // 3. (Pro Tip) Standard SPA behavior: Allow middle-clicks or Ctrl/Cmd clicks 
    // to open in a new tab normally.
    if (e.button !== 0 || e.ctrlKey || e.metaKey || e.shiftKey || e.altKey) return;

    console.log("SPA Navigation to:", link.getAttribute('href'));

    e.preventDefault();
    const url = link.getAttribute('href');

    window.history.pushState({}, '', url);
    router();
});

window.addEventListener('artifactory:navigated', () => {
    router();
});

// Listen for the custom event we dispatched in TransferManager
window.addEventListener('artifactory:refresh', (e) => {
    const updatedPath = e.detail.path;
    const currentPath = window.location.pathname;

    // Re-check UI permissions whenever data refreshes
    applyUIPermissions();

    // Only refresh if the user is currently looking at the folder where the file was uploaded
    // or if they are at the root.
    console.log("refresh event", currentPath, e);
    if (currentPath === updatedPath || currentPath === '/') {
        router(); // Re-run the router to fetch new data and re-render
    }
});

/**
 * Handle Auth Failures globally
 */
window.addEventListener('af:require-login', () => {
    console.warn("Authentication required - opening login dialog");

    // 1. Clear any stale user data
    localStorage.removeItem('af_token');
    localStorage.removeItem('af_user');

    // 2. Open the login dialog
    openLogin();
});

function applyUIPermissions() {
    const isLoggedIn = !!localStorage.getItem('af_token');
    const writeElements = document.querySelectorAll('.af-requires-auth');

    writeElements.forEach(el => {
        el.style.display = isLoggedIn ? '' : 'none';
    });
}

// Initialize the history/UI
TransferManager.init();
initGlobalSearch();

const settingsBtn = document.querySelector('[title="Settings"]');
if (settingsBtn) {
    settingsBtn.onclick = (e) => {
        e.preventDefault();
        openSettings();
    };
}

// Initial Load
router();