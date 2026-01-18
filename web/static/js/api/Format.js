
export const Format = {

    formatBytes(bytes) {
        if (bytes === 0) return '0 Bytes';
        const k = 1024;
        const sizes = ['Bytes', 'KB', 'MB', 'GB'];
        const i = Math.floor(Math.log(bytes) / Math.log(k));
        return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
    },

    getFileIcon(file) {
        if (file.isdir) return '📁';

        const ext = file.name.split('.').pop().toLowerCase();
        const icons = {
            pdf: '📕',
            zip: '📦', gz: '📦', tar: '📦', '7z': '📦',
            jpg: '🖼️', png: '🖼️', gif: '🖼️', svg: '🖼️',
            txt: '📄', log: '📄',
            go: '🐹',
            js: '📜', ts: '📜',
            html: '🌐',
            css: '🎨',
            sh: '🐚', bash: '🐚',
            json: '⚙️', yaml: '⚙️', yml: '⚙️'
        };
        return icons[ext] || '📄';
    },

    /**
     * Formats a date string for UI display.
     * Target: YYYY-MM-DD HH:mm:ss
     */
    dateTime(val) {
        if (!val || val.startsWith('0001-01-01')) {
            return '-';
        }

        const d = new Date(val);
        if (isNaN(d.getTime())) return '-';

        const pad = (n) => n.toString().padStart(2, '0');

        const date = [
            d.getFullYear(),
            pad(d.getMonth() + 1),
            pad(d.getDate())
        ].join('-');

        const time = [
            pad(d.getHours()),
            pad(d.getMinutes()),
            pad(d.getSeconds())
        ].join(':');

        return `${date} ${time}`;
    },

    /**
     * Formats a date string for <input type="datetime-local">
     * Requires: YYYY-MM-DDTHH:mm
     */
    toHTMLInput(val) {
        if (!val || val.startsWith('0001-01-01')) return '';
        const d = new Date(val); // Parses UTC or ISO string into local Date object
        if (isNaN(d.getTime())) return '';

        const pad = (n) => n.toString().padStart(2, '0');

        // Return the LOCAL time components
        const date = `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
        const time = `${pad(d.getHours())}:${pad(d.getMinutes())}`;

        return `${date}T${time}`;
    },

    /**
     * Checks if a date is within a certain threshold of hours from now.
     * Useful for "Warning" states.
     */
    isNearExpiry(val, thresholdHours = 24) {
        if (!val || val.startsWith('0001-01-01')) return false;

        const expiry = new Date(val);
        const now = new Date();
        const diff = expiry.getTime() - now.getTime();

        const thresholdMs = thresholdHours * 60 * 60 * 1000;

        // True if expiring in the future, but sooner than the threshold
        return diff > 0 && diff < thresholdMs;
    },

    /**
     * Checks if the date has already passed.
     * Useful for "Critical/Expired" states.
     */
    isExpired(val) {
        if (!val || val.startsWith('0001-01-01')) return false;
        return new Date(val) < new Date();
    },

    /**
     * Human readable relative time (simplified)
     */
    timeRemaining(val) {
        if (!val || val.startsWith('0001-01-01')) return '';

        const expiry = new Date(val);
        const now = new Date();
        const diffMs = expiry.getTime() - now.getTime();

        if (diffMs <= 0) return 'Expired';

        const diffMins = Math.floor(diffMs / (1000 * 60));
        const diffHours = Math.floor(diffMs / (1000 * 60 * 60));
        const diffDays = Math.floor(diffMs / (1000 * 60 * 60 * 24));

        // 1. Less than 1 hour: show minutes
        if (diffHours < 1) {
            // Ensure we don't show "0m" if there are seconds left
            return `${diffMins > 0 ? diffMins : '< 1'}m`;
        }

        // 2. More than 72 hours: show days
        if (diffHours >= 72) {
            return `${diffDays}d`;
        }

        // 3. Between 1 and 72 hours: show hours
        return `${diffHours}h`;
    },

    /**
     * Converts duration/datetime to format that backend can consume
     */
    durationToBackendFormat(input) {
        // 1. Try to let JavaScript parse it as a date
        const dateAttempt = new Date(input);

        // 2. Check if the date is valid. 
        // Note: We also check if the input contains a '-' to ensure 
        // strings like "100" aren't accidentally parsed as the year 100 AD.
        if (!isNaN(dateAttempt.getTime()) && input.includes('-')) {
            // If it's a valid date, convert to UTC ISO string ("2026-01-04T14:00:00.000Z")
            // This solves the 1-hour jump issue.
            return dateAttempt.toISOString();
        } else {
            // 3. Fallback: It's likely a duration like "7d", "1h", or "30m"
            // Send the raw string and let Go's ParseExpiry handle it.
            return input;
        }
    }
};