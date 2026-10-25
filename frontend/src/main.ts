import './style.css';

const button = document.querySelector<HTMLButtonElement>('#check-api')!;
const status = document.querySelector<HTMLParagraphElement>('#status')!;

button.addEventListener('click', async () => {
  button.disabled = true;
  status.textContent = 'Connecting…';
  try {
    const response = await fetch('/api/health', { signal: AbortSignal.timeout(5000) });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const data: unknown = await response.json();
    if (typeof data !== 'object' || data === null || !('status' in data) || data.status !== 'ok') {
      throw new Error('Unexpected API response');
    }
    status.textContent = 'Connected! The Go API is running.';
  } catch {
    status.textContent = 'Could not reach the API. Make sure the Go server is running.';
  } finally {
    button.disabled = false;
  }
});

const screens = ['main', 'uploads', 'videos'] as const;
function showScreen() {
  const current = location.hash === '#videos' ? 'videos' : location.hash === '#uploads' ? 'uploads' : 'main';
  for (const screen of screens) {
    document.getElementById(`${screen}-screen`)!.hidden = screen !== current;
    const link = document.querySelector<HTMLAnchorElement>(`[data-screen="${screen}"]`)!;
    if (screen === current) link.setAttribute('aria-current', 'page');
    else link.removeAttribute('aria-current');
  }
  if (current === 'uploads') void loadFiles();
  if (current === 'videos') void loadVideos();
  document.title = current === 'main' ? 'My website' : `${current === 'videos' ? 'Videos' : 'Uploads'} · My website`;
}
window.addEventListener('hashchange', showScreen);

const uploadForm = document.querySelector<HTMLFormElement>('#upload-form')!;
const fileInput = document.querySelector<HTMLInputElement>('#files')!;
const uploadButton = document.querySelector<HTMLButtonElement>('#upload-button')!;
const uploadStatus = document.querySelector<HTMLParagraphElement>('#upload-status')!;
const uploadResults = document.querySelector<HTMLUListElement>('#upload-results')!;

function formatDuration(seconds: number): string {
  const total = Math.max(0, Math.ceil(seconds));
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const remaining = total % 60;
  return hours ? `${hours}h ${minutes}m ${remaining}s` : minutes ? `${minutes}m ${remaining}s` : `${remaining}s`;
}

// XMLHttpRequest exposes upload progress events; fetch does not.
function sendUpload(url: string, body: FormData, onProgress: (event: ProgressEvent) => void, onSent: () => void): Promise<XMLHttpRequest> {
  return new Promise((resolve, reject) => {
    const request = new XMLHttpRequest();
    request.open('POST', url);
    request.upload.addEventListener('progress', onProgress);
    request.upload.addEventListener('load', onSent);
    request.addEventListener('load', () => resolve(request));
    request.addEventListener('error', () => reject(new Error('Connection lost. Please retry the upload.')));
    request.addEventListener('abort', () => reject(new Error('Upload canceled.')));
    request.send(body);
  });
}

let uploadInProgress = false;
async function uploadFiles(files: File[]) {
  if (uploadInProgress) {
    uploadStatus.textContent = 'An upload is already running. Wait for it to finish before adding more files.';
    return;
  }
  if (!files.length) return;
  uploadInProgress = true;
  uploadButton.disabled = true;
  fileInput.disabled = true;
  uploadResults.replaceChildren();
  const destination = currentFolder;
  let uploaded = 0;
  for (const [index, file] of files.entries()) {
    uploadStatus.textContent = `Uploading ${index + 1} of ${files.length}…`;
    const result = document.createElement('li');
    const label = document.createElement('p');
    label.textContent = file.name;
    const progress = document.createElement('progress');
    progress.max = 100;
    progress.value = 0;
    progress.setAttribute('aria-label', `Upload progress for ${file.name}`);
    const details = document.createElement('p');
    details.className = 'upload-details';
    result.append(label, progress, details);
    uploadResults.append(result);
    const started = performance.now();
    let sent = 0;
    let total = 0;
    let awaitingServer = false;
    const elapsed = () => (performance.now() - started) / 1000;
    const renderProgress = () => {
      const seconds = elapsed();
      const percent = total ? Math.min(100, Math.floor(sent / total * 100)) : 0;
      const remaining = sent > 0 && seconds >= 1 ? (total - sent) / (sent / seconds) : null;
      details.textContent = awaitingServer
        ? `Transfer sent · ${formatDuration(seconds)} elapsed · Waiting for server confirmation…`
        : `${percent}% · ${formatDuration(seconds)} elapsed · ${remaining !== null && total ? `About ${formatDuration(remaining)} remaining` : 'Estimating time remaining…'}`;
    };
    renderProgress();
    const timer = window.setInterval(renderProgress, 1000);
    try {
      const body = new FormData();
      body.append('file', file);
      const response = await sendUpload(folderURL('/api/uploads', destination), body, event => {
        sent = event.loaded;
        total = event.lengthComputable ? event.total : 0;
        if (total) progress.value = sent / total * 100;
        else progress.removeAttribute('value');
        renderProgress();
      }, () => {
        awaitingServer = true;
        progress.value = 100;
        renderProgress();
      });
      if (response.status < 200 || response.status >= 300) {
        const messages: Record<number, string> = {
          400: 'The server could not read the upload.',
          401: 'Sign in before uploading files.',
          403: 'The server denied this upload.',
          404: 'The upload endpoint was not found. Deploy the updated Go server and check API routing.',
          405: 'The server does not accept uploads here. Check the deployed Go server and API routing.',
          409: 'A file with this name already exists. Rename your file or delete the existing file first.',
          413: 'The server or proxy rejected the file size. Check the proxy upload limit.',
          500: 'The server could not save the file. Check its upload directory permissions, free disk space, and logs.',
          502: 'The API server is unavailable. Check that the Go server is running.',
          503: 'The API server is temporarily unavailable.',
          504: 'The server timed out while uploading the file.',
        };
        throw new Error(`Upload failed (HTTP ${response.status}). ${messages[response.status] ?? 'Check the server logs for details.'}`);
      }
      if (response.status !== 201 || !response.getResponseHeader('content-type')?.includes('application/json')) {
        throw new Error('Unexpected upload response. Check that /api/uploads is routed to the updated Go server.');
      }
      progress.value = 100;
      label.textContent = `${file.name} — uploaded successfully`;
      details.textContent = `Completed in ${formatDuration(elapsed())}.`;
      uploaded++;
    } catch (error) {
      label.textContent = `${file.name} — ${error instanceof Error ? error.message : 'Upload failed.'}`;
      details.textContent = `Failed after ${formatDuration(elapsed())}.`;
      result.className = 'upload-error';
    } finally {
      window.clearInterval(timer);
    }
  }
  uploadStatus.textContent = `${uploaded} of ${files.length} files uploaded.`;
  if (uploaded === files.length) uploadForm.reset();
  uploadButton.disabled = false;
  fileInput.disabled = false;
  uploadInProgress = false;
  await loadFiles();
}

uploadForm.addEventListener('submit', event => {
  event.preventDefault();
  void uploadFiles(Array.from(fileInput.files ?? []));
});

const uploadsScreen = document.querySelector<HTMLElement>('#uploads-screen')!;
let fileDragDepth = 0;
function isFileDrag(event: DragEvent): boolean {
  return Array.from(event.dataTransfer?.types ?? []).includes('Files');
}
function clearDropHighlight() {
  fileDragDepth = 0;
  uploadForm.classList.remove('drag-active');
}
window.addEventListener('dragenter', event => {
  if (uploadsScreen.hidden || !isFileDrag(event)) return;
  event.preventDefault();
  fileDragDepth++;
  if (!uploadInProgress) uploadForm.classList.add('drag-active');
});
window.addEventListener('dragover', event => {
  if (uploadsScreen.hidden || !isFileDrag(event)) return;
  event.preventDefault();
  if (event.dataTransfer) event.dataTransfer.dropEffect = uploadInProgress ? 'none' : 'copy';
});
window.addEventListener('dragleave', event => {
  if (!isFileDrag(event)) return;
  fileDragDepth = Math.max(0, fileDragDepth - 1);
  if (!fileDragDepth) clearDropHighlight();
});
window.addEventListener('drop', event => {
  clearDropHighlight();
  if (uploadsScreen.hidden || !isFileDrag(event)) return;
  event.preventDefault();
  const transfer = event.dataTransfer;
  if (!transfer) return;
  // Folder creation/navigation remains explicit; do not upload directory entries as files.
  if (Array.from(transfer.items).some(item => item.webkitGetAsEntry?.()?.isDirectory)) {
    uploadStatus.textContent = 'Please drop files only. Create or open a folder in the list before uploading into it.';
    return;
  }
  void uploadFiles(Array.from(transfer.files));
});
window.addEventListener('dragend', clearDropHighlight);
window.addEventListener('hashchange', clearDropHighlight);

const fileList = document.querySelector<HTMLTableSectionElement>('#file-list')!;
const filesStatus = document.querySelector<HTMLParagraphElement>('#files-status')!;
const refreshFiles = document.querySelector<HTMLButtonElement>('#refresh-files')!;
let loadingFiles = false;
let currentFolder = '';
const parentFolder = document.querySelector<HTMLButtonElement>('#parent-folder')!;
function folderURL(endpoint: string, path = currentFolder): string {
  return `${endpoint}?${new URLSearchParams({ path })}`;
}
function navigateFolder(path: string) {
  if (loadingFiles) return;
  currentFolder = path;
  document.querySelector('#current-folder')!.textContent = `Current folder: /${path}`;
  document.querySelector('#upload-destination')!.textContent = `Uploading to: /${path}`;
  parentFolder.disabled = !path;
  void loadFiles();
}
parentFolder.addEventListener('click', () => navigateFolder(currentFolder.split('/').slice(0, -1).join('/')));
const folderForm = document.querySelector<HTMLFormElement>('#folder-form')!;
const folderName = document.querySelector<HTMLInputElement>('#folder-name')!;
const createFolderButton = document.querySelector<HTMLButtonElement>('#create-folder')!;
const folderDialog = document.querySelector<HTMLDialogElement>('#folder-dialog')!;
const folderError = document.querySelector<HTMLParagraphElement>('#folder-error')!;
const cancelFolder = document.querySelector<HTMLButtonElement>('#cancel-folder')!;
let newFolderDestination = '';
document.querySelector('#new-folder')!.addEventListener('click', () => {
  newFolderDestination = currentFolder;
  folderForm.reset();
  folderError.textContent = '';
  document.querySelector('#folder-location')!.textContent = `Create in: /${newFolderDestination}`;
  folderDialog.showModal();
});
cancelFolder.addEventListener('click', () => folderDialog.close());
folderDialog.addEventListener('cancel', event => {
  if (createFolderButton.disabled) event.preventDefault();
});
folderForm.addEventListener('submit', async event => {
  event.preventDefault();
  const name = folderName.value;
  if (!name.trim() || name === '.' || name === '..' || /[/\\]/.test(name)) {
    folderError.textContent = 'Enter a single folder name without slashes.';
    return;
  }
  createFolderButton.disabled = true;
  cancelFolder.disabled = true;
  folderError.textContent = '';
  try {
    const response = await fetch(folderURL('/api/folders', newFolderDestination), {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name }), signal: AbortSignal.timeout(10000),
    });
    if (!response.ok) throw new Error(response.status === 409 ? 'A file or folder with this name already exists.' : `Could not create folder (HTTP ${response.status}).`);
    folderDialog.close();
    await loadFiles();
  } catch (error) {
    folderError.textContent = error instanceof Error ? error.message : 'Could not create folder.';
  } finally { createFolderButton.disabled = false; cancelFolder.disabled = false; }
});

const downloadSelected = document.querySelector<HTMLButtonElement>('#download-selected')!;
const selectAllFiles = document.querySelector<HTMLInputElement>('#select-all-files')!;
function updateDownloadSelection() {
  const boxes = Array.from(fileList.querySelectorAll<HTMLInputElement>('input[data-download-name]'));
  const selected = boxes.filter(box => box.checked).length;
  downloadSelected.disabled = !selected;
  downloadSelected.textContent = `Download selected as ZIP (${selected})`;
  selectAllFiles.disabled = !boxes.length;
  selectAllFiles.checked = boxes.length > 0 && selected === boxes.length;
  selectAllFiles.indeterminate = selected > 0 && selected < boxes.length;
}
selectAllFiles.addEventListener('change', () => {
  fileList.querySelectorAll<HTMLInputElement>('input[data-download-name]').forEach(box => { box.checked = selectAllFiles.checked; });
  updateDownloadSelection();
});
downloadSelected.addEventListener('click', () => {
  const boxes = Array.from(fileList.querySelectorAll<HTMLInputElement>('input[data-download-name]:checked'));
  if (!boxes.length) return;
  if (boxes.length > 200) { filesStatus.textContent = 'Select up to 200 files for a single ZIP.'; return; }
  // Submit natively so large ZIP files stream to the download manager, not browser memory.
  const form = document.createElement('form');
  form.method = 'POST';
  form.action = folderURL('/api/download-zip');
  form.target = '_blank';
  for (const box of boxes) {
    const input = document.createElement('input');
    input.type = 'hidden'; input.name = 'name'; input.value = box.dataset.downloadName!;
    form.append(input);
  }
  document.body.append(form);
  form.submit();
  form.remove();
});

const diskSpaceStatus = document.querySelector<HTMLParagraphElement>('#disk-space')!;
let loadingDiskSpace = false;
async function loadDiskSpace() {
  if (loadingDiskSpace) return;
  loadingDiskSpace = true;
  try {
    const response = await fetch(folderURL('/api/storage'), { cache: 'no-store', signal: AbortSignal.timeout(10000) });
    if (!response.ok) throw new Error('Disk space unavailable');
    const data = await response.json();
    if (!Number.isFinite(data.totalBytes) || data.totalBytes <= 0 ||
        !Number.isFinite(data.availableBytes) || data.availableBytes < 0 ||
        !Number.isFinite(data.freePercent) || data.freePercent < 0 || data.freePercent > 100) {
      throw new Error('Invalid disk space response');
    }
    const level = data.freePercent < 10 ? 'critical' : data.freePercent < 25 ? 'warning' : '';
    diskSpaceStatus.className = `disk-space ${level}`;
    const alert = level === 'critical' ? 'Critically low disk space. ' : level === 'warning' ? 'Low disk space. ' : '';
    diskSpaceStatus.textContent = `${alert}${formatSize(data.availableBytes)} free of ${formatSize(data.totalBytes)} (${data.freePercent.toFixed(1)}% free).`;
  } catch {
    diskSpaceStatus.className = 'disk-space';
    diskSpaceStatus.textContent = 'Disk space unavailable. Refresh to try again.';
  } finally {
    loadingDiskSpace = false;
  }
}
window.setInterval(() => {
  if (!uploadsScreen.hidden && !document.hidden) void loadDiskSpace();
}, 15000);

async function loadFiles() {
  if (loadingFiles) return;
  loadingFiles = true;
  void loadDiskSpace();
  refreshFiles.disabled = true;
  filesStatus.textContent = 'Loading files…';
  try {
    const response = await fetch(folderURL('/api/uploads'), { cache: 'no-store', signal: AbortSignal.timeout(10000) });
    if (!response.ok) throw new Error(`Could not load files (HTTP ${response.status}).`);
    const data: unknown = await response.json();
    if (!Array.isArray(data) || !data.every(file => file && typeof file.name === 'string' && typeof file.size === 'number' && typeof file.modified === 'string' && typeof file.isDirectory === 'boolean')) {
      throw new Error('Unexpected file list response. Check that the Go server has been restarted.');
    }
    fileList.replaceChildren();
    const listedFolder = currentFolder;
    data.sort((a, b) => Number(b.isDirectory) - Number(a.isDirectory) || a.name.localeCompare(b.name));
    for (const file of data) {
      const row = document.createElement('tr');
      for (const value of [file.name, file.isDirectory ? 'Folder' : formatSize(file.size), new Date(file.modified).toLocaleString()]) {
        const cell = document.createElement('td');
        if (file.isDirectory && row.children.length === 0) {
          const open = document.createElement('button');
          open.type = 'button';
          open.className = 'folder-link';
          open.textContent = file.name;
          open.setAttribute('aria-label', `Open folder ${file.name}`);
          open.addEventListener('click', () => navigateFolder([listedFolder, file.name].filter(Boolean).join('/')));
          cell.append(open);
        } else cell.textContent = value;
        row.append(cell);
      }
      const selection = document.createElement('td');
      if (!file.isDirectory) {
        const checkbox = document.createElement('input');
        checkbox.type = 'checkbox';
        checkbox.dataset.downloadName = file.name;
        checkbox.setAttribute('aria-label', `Select ${file.name} for download`);
        checkbox.addEventListener('change', updateDownloadSelection);
        selection.append(checkbox);
      }
      row.prepend(selection);
      const actions = document.createElement('td');
      actions.className = 'file-actions';
      if (!file.isDirectory) {
        const download = document.createElement('button');
        download.type = 'button';
        download.addEventListener('click', () => {
          window.location.assign(folderURL(`/api/downloads/${encodeURIComponent(file.name)}`, listedFolder));
        });
        download.textContent = 'Download';
        download.className = 'download-file';
        actions.append(download);
      }
      const remove = document.createElement('button');
      remove.type = 'button';
      remove.className = 'delete-file';
      remove.textContent = 'Delete';
      remove.setAttribute('aria-label', `Delete ${file.name}`);
      remove.addEventListener('click', async () => {
        const warning = file.isDirectory
          ? `WARNING: Permanently delete folder "${file.name}" and ALL files and subfolders inside it? This cannot be undone. Choose Cancel to keep the folder.`
          : `Permanently delete "${file.name}" from the server?`;
        if (!window.confirm(warning)) return;
        remove.disabled = true;
        try {
          const response = await fetch(folderURL(`/api/uploads/${encodeURIComponent(file.name)}`, listedFolder) + (file.isDirectory ? '&recursive=true' : ''), { method: 'DELETE', signal: AbortSignal.timeout(10000) });
          if (!response.ok) throw new Error(`Could not delete item (HTTP ${response.status}). Refresh the list and try again.`);
          await loadFiles();
          filesStatus.textContent = `Deleted ${file.name}.`;
        } catch (error) {
          filesStatus.textContent = error instanceof Error ? error.message : 'Could not delete item.';
        } finally {
          remove.disabled = false;
        }
      });
      actions.append(remove);
      row.append(actions);
      fileList.append(row);
    }
    filesStatus.textContent = data.length ? `${data.length} item${data.length === 1 ? '' : 's'}.` : 'This folder is empty.';
  } catch (error) {
    fileList.replaceChildren();
    filesStatus.textContent = error instanceof Error ? error.message : 'Could not load files.';
  } finally {
    loadingFiles = false;
    refreshFiles.disabled = false;
    updateDownloadSelection();
  }
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  if (bytes < 1024 ** 3) return `${(bytes / (1024 ** 2)).toFixed(1)} MB`;
  if (bytes < 1024 ** 4) return `${(bytes / (1024 ** 3)).toFixed(1)} GB`;
  return `${(bytes / (1024 ** 4)).toFixed(1)} TB`;
}
refreshFiles.addEventListener('click', () => void loadFiles());

const videoList = document.querySelector<HTMLUListElement>('#video-list')!;
const videoStatus = document.querySelector<HTMLParagraphElement>('#video-status')!;
const videoBack = document.querySelector<HTMLButtonElement>('#video-back')!;
const videoRefresh = document.querySelector<HTMLButtonElement>('#video-refresh')!;
const playSelected = document.querySelector<HTMLButtonElement>('#play-selected')!;
const clearVideos = document.querySelector<HTMLButtonElement>('#clear-videos')!;
const player = document.querySelector<HTMLVideoElement>('#video-player')!;
const videoFrame = document.querySelector<HTMLDivElement>('#video-frame')!;
const playerSizeButtons = document.querySelectorAll<HTMLButtonElement>('[data-player-size]');
function updatePlayerWindowWidth() {
  videoFrame.style.setProperty('--window-width', `${document.documentElement.clientWidth}px`);
}
for (const button of playerSizeButtons) {
  button.addEventListener('click', () => {
    videoFrame.style.setProperty('--player-scale', String(Number(button.dataset.playerSize) / 100));
    for (const option of playerSizeButtons) option.setAttribute('aria-pressed', String(option === button));
    updatePlayerWindowWidth();
  });
}
window.addEventListener('resize', updatePlayerWindowWidth);
updatePlayerWindowWidth();
const playlist = document.querySelector<HTMLOListElement>('#video-playlist')!;
const selectedVideos = new Map<string, { name: string; url: string }>();
let selectionSaves: Promise<void> = Promise.resolve();
let pendingSelectionSaves = 0;
let selectionRevision = 0;
let selectionRefreshRunning = false;
let videoFolder = '';
let videoLoading = false;
let selectionReady = false;
let playingQueue: { name: string; url: string }[] = [];
let playingIndex = 0;

async function restoreVideoSelection(synchronize = false) {
  const revision = selectionRevision;
  try {
    const response = await fetch('/api/video-selection', { cache: 'no-store', signal: AbortSignal.timeout(10000) });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const names: unknown = await response.json();
    if (!Array.isArray(names) || !names.every(name => typeof name === 'string')) throw new Error('Invalid selection response');
    // A poll started before a checkbox change must not replace that newer change.
    if (synchronize && (pendingSelectionSaves || revision !== selectionRevision)) return true;
    const previous = JSON.stringify(Array.from(selectedVideos.values(), video => video.name));
    selectedVideos.clear();
    for (const name of names) {
      if (typeof name !== 'string' || name.includes('\\') || name.includes('\0')) continue;
      const parts = name.split('/');
      if (parts.some(part => !part || part === '.' || part === '..')) continue;
      const filename = parts.pop()!;
      const url = folderURL(`/api/videos/${encodeURIComponent(filename)}`, parts.join('/'));
      selectedVideos.set(url, { name, url });
    }
    if (synchronize) {
      const changed = previous !== JSON.stringify(Array.from(selectedVideos.values(), video => video.name));
      if (changed) {
        updateVideoSelection(false);
        // Update current checkboxes without interrupting the player or folder navigation.
        for (const checkbox of videoList.querySelectorAll<HTMLInputElement>('input[data-stream-url]')) {
          checkbox.checked = selectedVideos.has(checkbox.dataset.streamUrl!);
        }
      }
    }
  } catch {
    document.querySelector('#selection-status')!.textContent = 'Could not load the server selection. Synchronization will retry automatically.';
    return false;
  }
  document.querySelector('#selection-status')!.textContent = 'Selection loaded from the server.';
  return true;
}
function updateVideoSelection(save = true) {
  if (save) {
    const names = Array.from(selectedVideos.values(), video => video.name);
    selectionRevision++;
    pendingSelectionSaves++;
    document.querySelector('#selection-status')!.textContent = 'Saving selection…';
    selectionSaves = selectionSaves.then(async () => {
      try {
        const response = await fetch('/api/video-selection', {
          method: 'PUT', headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(names), signal: AbortSignal.timeout(10000),
        });
        if (!response.ok) throw new Error(`HTTP ${response.status}`);
        document.querySelector('#selection-status')!.textContent = 'Selection saved on the server.';
      } catch {
        document.querySelector('#selection-status')!.textContent = 'Could not save selection on the server. Change the selection again to retry.';
      } finally { pendingSelectionSaves--; }
    });
  }
  document.querySelector('#video-count')!.textContent = `${selectedVideos.size} selected`;
  playSelected.disabled = clearVideos.disabled = selectedVideos.size === 0;
  playlist.replaceChildren();
  for (const video of selectedVideos.values()) {
    const item = document.createElement('li');
    const link = document.createElement('a');
    link.href = video.url;
    link.textContent = video.name;
    link.target = '_blank';
    link.rel = 'noopener';
    link.setAttribute('aria-label', `Open stream for ${video.name}`);
    const playHere = document.createElement('button');
    playHere.type = 'button';
    playHere.textContent = 'Play here';
    playHere.setAttribute('aria-label', `Play ${video.name} here`);
    playHere.addEventListener('click', () => {
      playingQueue = Array.from(selectedVideos.values());
      void playVideo(playingQueue.findIndex(entry => entry.url === video.url));
    });
    link.textContent = `${video.name} (stream link)`;
    item.append(playHere, document.createTextNode(' '), link);
    playlist.append(item);
  }
}
async function loadVideos(path = videoFolder) {
  if (!selectionReady) return;
  if (videoLoading) return;
  videoLoading = true;
  videoRefresh.disabled = true;
  videoStatus.textContent = 'Loading videos…';
  try {
    const response = await fetch(folderURL('/api/uploads', path), { cache: 'no-store', signal: AbortSignal.timeout(10000) });
    if (!response.ok) throw new Error(`Could not load videos (HTTP ${response.status}).`);
    const files: unknown = await response.json();
    if (!Array.isArray(files) || !files.every(file => file && typeof file.name === 'string' && typeof file.isDirectory === 'boolean')) throw new Error('Unexpected video list response.');
    videoFolder = path;
    videoBack.disabled = !path;
    document.querySelector('#video-folder')!.textContent = `Current folder: /${path}`;
    videoList.replaceChildren();
    const entries = files.filter(file => file.isDirectory || /\.(mp4|m4v|webm|ogv|ogg|mov|mkv)$/i.test(file.name));
    entries.sort((a, b) => Number(b.isDirectory) - Number(a.isDirectory) || a.name.localeCompare(b.name));
    for (const file of entries) {
      const item = document.createElement('li');
      if (file.isDirectory) {
        const open = document.createElement('button');
        open.type = 'button';
        open.className = 'folder-link';
        open.textContent = `Folder: ${file.name}`;
        open.addEventListener('click', () => void loadVideos([path, file.name].filter(Boolean).join('/')));
        item.append(open);
      } else {
        const url = folderURL(`/api/videos/${encodeURIComponent(file.name)}`, path);
        const label = document.createElement('label');
        const checkbox = document.createElement('input');
        checkbox.type = 'checkbox';
        checkbox.dataset.streamUrl = url;
        checkbox.checked = selectedVideos.has(url);
        checkbox.addEventListener('change', () => {
          if (checkbox.checked) selectedVideos.set(url, { name: [path, file.name].filter(Boolean).join('/'), url });
          else selectedVideos.delete(url);
          updateVideoSelection();
        });
        label.append(checkbox, document.createTextNode(` ${file.name}`));
        item.append(label);
      }
      videoList.append(item);
    }
    videoStatus.textContent = entries.length ? 'Select videos to stream. Selections are kept when you change folders.' : 'No videos or folders here.';
  } catch (error) {
    videoStatus.textContent = error instanceof Error ? error.message : 'Could not load videos.';
  } finally { videoLoading = false; videoRefresh.disabled = false; }
}
const audioTrack = document.querySelector<HTMLSelectElement>('#audio-track')!;
const subtitleTrack = document.querySelector<HTMLSelectElement>('#subtitle-track')!;
const trackStatus = document.querySelector<HTMLParagraphElement>('#media-track-status')!;
let activeMediaURL = '';
let mediaGeneration = 0;
function trackURL(base: string, action: string, index?: string) {
  const url = new URL(base, location.href);
  url.pathname += `/${action}`;
  if (index !== undefined) url.searchParams.set('track', index);
  return url.pathname + url.search;
}
function resetMediaTracks() {
  mediaGeneration++;
  activeMediaURL = '';
  player.querySelectorAll('track').forEach(track => track.remove());
  audioTrack.disabled = true;
  subtitleTrack.disabled = true;
  audioTrack.replaceChildren(new Option('Original audio', ''));
  subtitleTrack.replaceChildren(new Option('Off', ''));
  document.querySelector<HTMLElement>('#media-track-controls')!.hidden = false;
  trackStatus.textContent = 'Loading embedded tracks…';
}
async function loadMediaTracks(base: string) {
  activeMediaURL = base;
  const generation = mediaGeneration;
  try {
    const response = await fetch(trackURL(base, 'tracks'), { signal: AbortSignal.timeout(35000) });
    if (!response.ok) throw new Error(await response.text());
    const tracks: { index: number; codec_type: string; codec_name: string; supported: boolean; tags?: Record<string, string> }[] = await response.json();
    if (generation !== mediaGeneration) return;
    for (const track of tracks) {
      const label = [track.tags?.title, track.tags?.language, track.codec_name, `Track ${track.index}`].filter(Boolean).join(' · ');
      const option = new Option(label + (track.supported ? '' : ' (image subtitles unsupported)'), String(track.index));
      option.disabled = !track.supported;
      (track.codec_type === 'audio' ? audioTrack : subtitleTrack).append(option);
    }
    audioTrack.disabled = audioTrack.options.length <= 1;
    subtitleTrack.disabled = subtitleTrack.options.length <= 1;
    trackStatus.textContent = 'Audio selection prepares a cached copy with AAC audio. The first preparation may take a few minutes and use additional disk space. Image subtitles cannot be displayed here.';
  } catch (error) {
    if (generation === mediaGeneration) trackStatus.textContent = error instanceof Error ? error.message : 'Could not load tracks.';
  }
}
audioTrack.addEventListener('change', () => {
  if (!activeMediaURL) return;
  const time = player.currentTime;
  const paused = player.paused;
  const generation = mediaGeneration;
  player.src = audioTrack.value ? trackURL(activeMediaURL, 'audio', audioTrack.value) : activeMediaURL;
  trackStatus.textContent = 'Preparing audio track… Playback will resume when ready.';
  player.addEventListener('loadedmetadata', () => {
    if (generation !== mediaGeneration) return;
    if (Number.isFinite(time)) player.currentTime = time;
    if (!paused) void player.play().catch(() => {});
    trackStatus.textContent = 'Audio track ready.';
  }, { once: true });
});
subtitleTrack.addEventListener('change', () => {
  player.querySelectorAll('track').forEach(track => track.remove());
  if (!subtitleTrack.value || !activeMediaURL) return;
  const track = document.createElement('track');
  track.kind = 'subtitles';
  track.label = subtitleTrack.selectedOptions[0].text;
  track.src = trackURL(activeMediaURL, 'subtitles', subtitleTrack.value);
  track.default = true;
  track.addEventListener('load', () => { track.track.mode = 'showing'; });
  track.addEventListener('error', () => { trackStatus.textContent = 'Could not extract subtitles. Check the server FFmpeg installation and logs.'; });
  player.append(track);
  track.track.mode = 'showing';
});

async function playVideo(index: number) {
  const video = playingQueue[index];
  if (!video) return;
  playingIndex = index;
  player.hidden = false;
  resetMediaTracks();
  player.src = video.url;
  void loadMediaTracks(video.url);
  document.querySelector('#now-playing')!.textContent = `Playing ${index + 1} of ${playingQueue.length}: ${video.name}`;
  try { await player.play(); }
  catch { document.querySelector('#now-playing')!.textContent = `Ready: ${video.name}. Use Play, or open its stream link in a compatible player.`; }
}
playSelected.addEventListener('click', () => { playingQueue = Array.from(selectedVideos.values()); void playVideo(0); });
player.addEventListener('ended', () => { if (playingIndex + 1 < playingQueue.length) void playVideo(playingIndex + 1); });
player.addEventListener('error', () => { document.querySelector('#now-playing')!.textContent = 'Unable to play this video. Check its browser compatibility and that the file still exists.'; });
videoBack.addEventListener('click', () => void loadVideos(videoFolder.split('/').slice(0, -1).join('/')));
videoRefresh.addEventListener('click', () => { void synchronizeSelection(); void loadVideos(); });
clearVideos.addEventListener('click', () => { selectedVideos.clear(); updateVideoSelection(); void loadVideos(); });
async function initializeVideoSelection() {
  const restored = await restoreVideoSelection();
  updateVideoSelection(false);
  if (restored) { selectionReady = true; showScreen(); }
  else {
    // Avoid overwriting a saved playlist with an empty selection after a load failure.
    videoStatus.textContent = 'Reload the page to load the saved selection before editing it.';
  }
}
async function synchronizeSelection() {
  if (selectionRefreshRunning || pendingSelectionSaves || document.hidden) return;
  selectionRefreshRunning = true;
  try {
    if (!selectionReady) await initializeVideoSelection();
    else await restoreVideoSelection(true);
  } finally { selectionRefreshRunning = false; }
}
window.setInterval(() => void synchronizeSelection(), 3000);
window.addEventListener('focus', () => void synchronizeSelection());
document.addEventListener('visibilitychange', () => { if (!document.hidden) void synchronizeSelection(); });
void synchronizeSelection();
