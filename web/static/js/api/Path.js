export const Path = {
    /**
     * Safely joins and encodes path segments.
     * Example: join('docs', 'my%20file.txt') -> "docs/my%2520file.txt"
     */
    join(...segments) {
        return segments
            .map(s => s.toString().split('/')) // Handle segments that might already contain slashes
            .flat()
            .filter(s => s !== '' && s !== '.') // Remove empty/dot segments
            .map(segment => encodeURIComponent(segment)) // Encode only the individual name
            .join('/');
    },

    /**
     * Ensures a path starts with a leading slash and is properly encoded
     */
    toUrl(path) {
        if (!path || path === '/') return '/';
        const encoded = this.join(path);
        return '/' + encoded;
    }
};