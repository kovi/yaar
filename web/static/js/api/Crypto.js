let warned = false;

function warnOnce() {
    if (!warned) {
        console.warn(
            "[uuid] Falling back to insecure Math.random() UUID generator. " +
            "Use HTTPS to enable crypto.randomUUID() for better security."
        );
        warned = true;
    }
}

export function uuid() {
    // Best case
    if (window.crypto?.randomUUID) {
        return crypto.randomUUID();
    }

    // Second best
    if (window.crypto?.getRandomValues) {
        return ([1e7] + -1e3 + -4e3 + -8e3 + -1e11).replace(/[018]/g, c =>
            (c ^ crypto.getRandomValues(new Uint8Array(1))[0] & 15 >> c / 4).toString(16)
        );
    }

    // Last resort (not cryptographically secure)
    warnOnce();
    return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, c => {
        const r = Math.random() * 16 | 0;
        const v = c === 'x' ? r : (r & 0x3 | 0x8);
        return v.toString(16);
    });
};

