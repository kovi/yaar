export const LABEL_TEMPLATE = `
                    <label>
                    <span>Expiration</span>
                    <div class="af-input-with-action">
                        <input type="text" name="expires" id="expiry-text" placeholder="e.g. 7d or pick a date">
                        
                        <!-- The visual button -->
                        <button type="button" class="af-input-icon-btn" id="expiry-picker-trigger" title="Open Calendar">📅</button>
                        
                        <!-- Hidden native picker -->
                        <input type="date" id="expiry-hidden-picker" style="opacity:0; position:absolute; left:0; top:0; width:100%; height:100%; pointer-events:none;">
                    </div>
                    <small class="af-input-help">Use durations (7d, 24h) or select a date.</small>
                </label>
    `;

export function setValue(dialog, value) {
    const hiddenPicker = dialog.querySelector('#expiry-hidden-picker');
    const expiryDate = value ? value.split('T')[0] : '';
    hiddenPicker.value = expiryDate;
}

export function setupExpiryPicker(dialog) {
    const textInput = dialog.querySelector('#expiry-text');
    const hiddenPicker = dialog.querySelector('#expiry-hidden-picker');
    const trigger = dialog.querySelector('#expiry-picker-trigger');

    const updateFromPicker = () => {
        if (hiddenPicker.value) {
            // hiddenPicker.value is "YYYY-MM-DD"
            // We append a default time so the user doesn't have to
            const selectedDate = hiddenPicker.value;
            const defaultTime = "23:59:59";

            textInput.value = `${selectedDate} ${defaultTime}`;

            // Visual feedback
            textInput.classList.add('af-highlight-input');
            setTimeout(() => textInput.classList.remove('af-highlight-input'), 500);

            // Trigger events for other listeners
            textInput.dispatchEvent(new Event('input', { bubbles: true }));
        }
    };

    // 'input' is the most reliable event for native pickers in Firefox
    hiddenPicker.addEventListener('input', updateFromPicker);

    trigger.onclick = (e) => {
        e.preventDefault();
        if (typeof hiddenPicker.showPicker === 'function') {
            hiddenPicker.showPicker();
        } else {
            hiddenPicker.click();
        }
    };
}