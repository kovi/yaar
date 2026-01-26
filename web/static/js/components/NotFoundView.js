export function NotFoundView(requestedPath) {
    console.log("not found", requestedPath)
    const container = document.createElement('div');
    container.className = 'af-card';
    container.style.maxWidth = '600px';
    container.style.margin = '40px auto';
    container.style.textAlign = 'center';

    // Calculate parent directory
    const parts = requestedPath.split('/').filter(p => p);
    const parentPath = parts.length > 0 ? '/' + parts.slice(0, -1).join('/') : '/';
    const readablePath = decodeURIComponent(requestedPath);

    container.innerHTML = `
        <div style="font-size: 48px; margin-bottom: 20px;">🕵️‍♂️</div>
        <h2 style="margin-bottom: 10px;">Resource Not Found</h2>
        <p class="af-text-muted" style="margin-bottom: 24px;">
            The path <code>${readablePath}</code> does not exist on this server.
        </p>
        
        <div class="af-alert af-alert-info" style="justify-content: center; margin-bottom: 30px;">
            The file might have been moved or deleted.
        </div>

        <div style="display: flex; gap: 12px; justify-content: center;">
            <a href="${parentPath}" class="btn btn-ghost nav-link">⤴️ Back to Parent</a>
            <a href="/" class="btn btn-primary nav-link">🏠 Go to Root</a>
        </div>
    `;

    return container;
}