export function initRouter(onRoute) {
    window.addEventListener('popstate', () => {
        onRoute(window.location.pathname);
    });

    // Capture all clicks on links with .nav-link
    document.body.addEventListener('click', e => {
        if (e.target.classList.contains('nav-link')) {
            e.preventDefault();
            const href = e.target.getAttribute('href');
            window.history.pushState({}, '', href);
            onRoute(href);
        }
    });
}