// One modal drives both backup flows: choosing which of the six domains
// (see routes/backup.js's BACKUP_SECTIONS) to export, and — for restore —
// previewing exactly what a file would change before anything is written.
// A single shared implementation instead of two separate ones keeps the
// section list and its labels from drifting apart between export and
// restore, the same reasoning `lib/machines/options-adoption.js` documents
// for tracked options.
import { t } from '../i18n.js';
import { apiFetch, initToken } from '../api.js';
import { shareOrDownloadBlob } from '../utils.js';

const SECTION_KEYS = ['shots', 'maintenance', 'orders', 'machines', 'settings', 'secrets'];

// Which top-level backup keys prove a given section actually has data in a
// file being restored — mirrors routes/backup.js's SECTION_BUNDLE_KEYS.
// Used only to decide which restore checkboxes to offer; export always
// offers all six regardless of whether the *current* install has data in
// them (an empty section is still a valid, deliberate choice to make).
const SECTION_PRESENCE_KEYS = {
    shots:       ['shots'],
    maintenance: ['maintenance', 'maintenance_log'],
    orders:      ['orders'],
    machines:    ['machines'],
    settings:    ['kv'],
    secrets:     ['secrets'],
};

let mode = null;       // 'export' | 'restore'
let restoreBundle = null;
let previewDebounce = null;

function els() {
    return {
        modal:        document.getElementById('backupModal'),
        title:        document.getElementById('backupModalTitle'),
        desc:         document.getElementById('backupModalDesc'),
        sectionsBox:  document.getElementById('backupModalSections'),
        secretsRow:   document.getElementById('backupSecretsRow'),
        secretsCb:    document.getElementById('backupSecretsCb'),
        passRow:      document.getElementById('backupPassphraseRow'),
        passInput:    document.getElementById('backupPassphraseInput'),
        passConfirm:  document.getElementById('backupPassphraseConfirm'),
        passConfirmRow: document.getElementById('backupPassphraseConfirmRow'),
        preview:      document.getElementById('backupPreview'),
        error:        document.getElementById('backupModalError'),
        confirmBtn:   document.getElementById('backupModalConfirmBtn'),
        cancelBtn:    document.getElementById('backupModalCancelBtn'),
    };
}

function checkedSections() {
    return [...document.querySelectorAll('.backup-section-cb')]
        .filter(cb => !cb.disabled && cb.checked)
        .map(cb => cb.value);
}

function setError(msg) {
    const { error } = els();
    error.textContent = msg || '';
    error.style.display = msg ? '' : 'none';
}

function closeBackupModal() {
    els().modal.classList.remove('open');
    mode = null;
    restoreBundle = null;
    clearTimeout(previewDebounce);
}

function renderSectionCheckboxes(presentSections) {
    const { sectionsBox } = els();
    sectionsBox.innerHTML = '';
    for (const key of SECTION_KEYS) {
        if (key === 'secrets') continue; // rendered separately below, it needs the passphrase row next to it
        const present = !presentSections || presentSections.has(key);
        const label = document.createElement('label');
        label.className = 'backup-section-row';
        label.innerHTML = `<input type="checkbox" class="backup-section-cb" value="${key}" ${present ? 'checked' : 'disabled'}>`
            + `<span>${t(`backup_section_${key}`)}</span>`
            + (present ? '' : `<span class="backup-section-empty">${t('backup_section_empty')}</span>`);
        sectionsBox.appendChild(label);
    }
}

// Only meaningful for restore: calls the dry-run path so the preview shown
// to the user is computed by the exact same sanitizers/schemas the real
// restore uses, instead of a second hand-rolled estimate that could drift
// out of sync with what actually gets applied.
async function refreshRestorePreview() {
    if (mode !== 'restore' || !restoreBundle) return;
    const { preview } = els();
    const sections = checkedSections();
    const passphrase = els().secretsCb.checked ? els().passInput.value : undefined;
    try {
        const r = await apiFetch('api/restore', {
            method: 'POST', headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ ...restoreBundle, dryRun: true, sections, passphrase }),
        });
        const body = await r.json();
        if (!r.ok || !body.preview) { preview.textContent = ''; return; }
        const p = body.preview;
        const lines = [];
        if (sections.includes('shots'))       lines.push(t('backup_preview_shots', p.shots) + (p.library ? ` · ${t('backup_preview_library')}` : ''));
        if (sections.includes('maintenance')) lines.push(t('backup_preview_maintenance', p.maintenance, p.maintenanceTotal) + ', ' + t('backup_preview_maintenance_log', p.maintenanceLog, p.maintenanceLogTotal));
        if (sections.includes('orders'))      lines.push(t('backup_preview_orders', p.orders, p.ordersTotal));
        if (sections.includes('machines'))    lines.push(t('backup_preview_machines', p.machines));
        if (sections.includes('settings') && p.settings) lines.push(t('backup_preview_settings'));
        if (p.images) lines.push(t('backup_preview_images', p.images));
        if (els().secretsCb.checked) {
            lines.push(p.secretsPresent
                ? (p.secretsRestored ? t('backup_preview_secrets_ok') : t('backup_preview_secrets_wrong'))
                : t('backup_preview_secrets_none'));
        }
        preview.innerHTML = lines.map(l => `<div>${l}</div>`).join('');
    } catch { preview.textContent = ''; }
}

function scheduleRestorePreview() {
    clearTimeout(previewDebounce);
    previewDebounce = setTimeout(refreshRestorePreview, 250);
}

export function openBackupExportModal() {
    mode = 'export';
    const { modal, title, desc, secretsRow, passRow, passConfirmRow, preview, confirmBtn, cancelBtn } = els();
    title.textContent = t('backup_modal_export_title');
    desc.textContent  = t('backup_modal_export_desc');
    renderSectionCheckboxes(null);
    secretsRow.style.display = '';
    els().secretsCb.checked = false;
    passRow.style.display = 'none';
    passConfirmRow.style.display = 'none';
    preview.style.display = 'none';
    preview.innerHTML = '';
    setError('');
    confirmBtn.textContent = t('backup_modal_export_confirm');
    modal.classList.add('open');

    els().secretsCb.onchange = () => { passRow.style.display = els().secretsCb.checked ? '' : 'none'; passConfirmRow.style.display = els().secretsCb.checked ? '' : 'none'; };
    cancelBtn.onclick = closeBackupModal;
    confirmBtn.onclick = async () => {
        const sections = checkedSections();
        if (!sections.length) { setError(t('backup_error_no_sections')); return; }
        const wantsSecrets = els().secretsCb.checked;
        const passphrase = els().passInput.value;
        if (wantsSecrets && !passphrase) { setError(t('backup_error_passphrase_required')); return; }
        if (wantsSecrets && passphrase !== els().passConfirm.value) { setError(t('backup_error_passphrase_mismatch')); return; }
        setError('');
        try {
            const r = await apiFetch('api/backup', {
                method: 'POST', headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ sections, passphrase: wantsSecrets ? passphrase : undefined }),
            });
            if (!r.ok) { const err = await r.json().catch(() => ({})); setError(t('backup_error', err.error || r.status)); return; }
            const bundle   = await r.json();
            const blob     = new Blob([JSON.stringify(bundle, null, 2)], { type: 'application/json' });
            const filename = `glp-backup-${new Date().toISOString().slice(0, 10)}.json`;
            await shareOrDownloadBlob(blob, filename, { title: filename });
            closeBackupModal();
        } catch (e) { setError(t('backup_error', e.message)); }
    };
}

// `input` is the file <input> element restoreFromFile() was originally
// wired to, so this can reset it (input.value = '') the same way the old
// direct-restore flow always did, on every exit path.
export async function openBackupRestoreModal(input) {
    const file = input.files[0];
    if (!file) return;
    try {
        const text = await file.text();
        const bundle = JSON.parse(text);
        if (!bundle.glp_backup) {
            alert(t('backup_invalid'));
            // eslint-disable-next-line require-atomic-updates -- `input` is the caller's DOM element, not shared module state; nothing else writes input.value concurrently
            input.value = '';
            return;
        }
        restoreBundle = bundle;
    } catch (e) {
        alert(t('backup_error', e.message));
        // eslint-disable-next-line require-atomic-updates -- see above
        input.value = '';
        return;
    }

    mode = 'restore';
    const present = new Set(SECTION_KEYS.filter(key => SECTION_PRESENCE_KEYS[key].some(k => k in restoreBundle)));
    const { modal, title, desc, secretsRow, passRow, passConfirmRow, preview, confirmBtn, cancelBtn } = els();
    title.textContent = t('backup_modal_restore_title');
    desc.textContent  = t('backup_modal_restore_desc');
    renderSectionCheckboxes(present);
    const hasSecrets = present.has('secrets');
    secretsRow.style.display = hasSecrets ? '' : 'none';
    els().secretsCb.checked = hasSecrets;
    passRow.style.display = hasSecrets ? '' : 'none';
    passConfirmRow.style.display = 'none'; // restore only needs the passphrase once, no confirm field
    preview.style.display = '';
    preview.innerHTML = '';
    setError('');
    confirmBtn.textContent = t('backup_modal_restore_confirm');
    modal.classList.add('open');

    for (const cb of document.querySelectorAll('.backup-section-cb')) cb.onchange = scheduleRestorePreview;
    els().secretsCb.onchange = () => { passRow.style.display = els().secretsCb.checked ? '' : 'none'; scheduleRestorePreview(); };
    els().passInput.oninput = scheduleRestorePreview;
    cancelBtn.onclick = () => { input.value = ''; closeBackupModal(); };
    confirmBtn.onclick = async () => {
        const sections = checkedSections();
        if (!sections.length) { setError(t('backup_error_no_sections')); return; }
        const passphrase = els().secretsCb.checked ? els().passInput.value : undefined;
        setError('');
        try {
            const r = await apiFetch('api/restore', {
                method: 'POST', headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ ...restoreBundle, sections, passphrase }),
            });
            const res = await r.json();
            if (!res.ok) { setError(t('backup_error', res.error)); return; }
            // The restore may have just replaced the API token this session is
            // using -- /api/token serves any caller that can reach the port
            // (see routes/system.js), so re-fetching it is always safe and,
            // if it changed, required before any further apiFetch() call.
            if (res.secretsPresent && res.secretsRestored) await initToken();
            input.value = '';
            closeBackupModal();
            if (window.loadData) await window.loadData();
            alert(res.secretsPresent
                ? (res.secretsRestored ? t('backup_restored_with_secrets', res.shots) : t('backup_restored_secrets_failed', res.shots))
                : t('backup_restored', res.shots));
        } catch (e) { setError(t('backup_error', e.message)); }
    };

    refreshRestorePreview();
}

export { closeBackupModal };
