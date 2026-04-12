const NOTES_STORAGE_KEY = "pwa-notes-app.notes";
const CLIENT_ID_STORAGE_KEY = "pwa-notes-app.client-id";
const VAPID_PUBLIC_KEY_STORAGE_KEY = "pwa-notes-app.vapid-public-key";
const TOAST_DURATION = 4200;

const state = {
  appConfigPromise: null,
  currentPage: "home",
  registrationPromise: null,
  subscriptionSyncedEndpoint: "",
  socket: null,
  socketToken: 0,
  reconnectTimer: null,
  reconnectDelay: 1000,
  pendingRealtimeTasks: [],
  reminderSyncPromise: null,
};

document.addEventListener("DOMContentLoaded", () => {
  bindShellNavigation();
  bindServiceWorkerMessages();
  window.addEventListener("hashchange", handleRouteChange);
  window.addEventListener("storage", handleStorageSync);
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible") {
      void syncCurrentPushSubscription();
      if (state.currentPage === "home") {
        void syncPushControls();
      }
    }
  });
  window.addEventListener("online", () => {
    updateConnectionStatus("Возвращаем WebSocket...", "neutral");
    initRealtime(true);
    if (state.currentPage === "home") {
      void syncFutureRemindersWithServer();
      void syncPushControls();
    }
    void syncCurrentPushSubscription();
  });
  window.addEventListener("offline", () => {
    clearTimeout(state.reconnectTimer);
    state.reconnectTimer = null;
    updateConnectionStatus("Нет сети", "offline");
  });

  void registerServiceWorker();
  void getAppConfig();
  void syncCurrentPushSubscription();
  initRealtime();
  handleRouteChange();
});

function bindShellNavigation() {
  if (window.location.hash !== "#home") {
    window.location.hash = "#home";
  }
}

function bindServiceWorkerMessages() {
  if (!("serviceWorker" in navigator)) {
    return;
  }

  navigator.serviceWorker.addEventListener("message", handleServiceWorkerMessage);
}

function handleRouteChange() {
  if (window.location.hash !== "#home") {
    window.location.hash = "#home";
    return;
  }

  void loadHomeContent();
}

async function loadHomeContent() {
  const target = document.querySelector("#page-container");
  if (!target) {
    return;
  }

  try {
    const response = await fetch("/content/home.html", { headers: { Accept: "text/html" } });
    if (!response.ok) {
      throw new Error(`HTTP ${response.status}`);
    }

    target.innerHTML = await response.text();
    state.currentPage = "home";
    initNotes();
    void initPushControls();
    void syncFutureRemindersWithServer();
  } catch (error) {
    console.error("Failed to load dynamic content", error);
    target.innerHTML = `
      <section class="card">
        <h2>Заметки</h2>
      </section>
    `;
    showToast("Не удалось загрузить страницу.", "error");
  }
}

function initNotes() {
  renderNotes();

  const noteForm = document.querySelector("#note-form");
  const noteInput = document.querySelector("#note-text");
  const reminderForm = document.querySelector("#reminder-form");
  const reminderText = document.querySelector("#reminder-text");
  const reminderTime = document.querySelector("#reminder-time");

  if (noteForm && noteInput) {
    noteForm.addEventListener("submit", async (event) => {
      event.preventDefault();

      const created = await addNote({ text: noteInput.value });
      if (!created) {
        return;
      }

      noteInput.value = "";
    });
  }

  if (reminderForm && reminderText && reminderTime) {
    reminderForm.addEventListener("submit", async (event) => {
      event.preventDefault();

      const text = reminderText.value.trim();
      const reminderTimestamp = parseReminderInput(reminderTime.value);

      if (!text) {
        showToast("Введите текст напоминания.", "warning");
        return;
      }
      if (!reminderTime.value) {
        showToast("Укажите дату и время напоминания.", "warning");
        return;
      }
      if (!reminderTimestamp || reminderTimestamp <= Date.now()) {
        showToast("Напоминание должно быть строго в будущем.", "warning");
        return;
      }

      if (supportsPushFeatures()) {
        const permission = await ensurePushReadyFromUserGesture();
        if (permission === "blocked") {
          showToast("Разрешите уведомления в браузере, иначе push-напоминание не придет.", "warning");
        }
      }

      const created = await addNote({ text, reminder: reminderTimestamp });
      if (!created) {
        return;
      }

      reminderText.value = "";
      reminderTime.value = "";
    });
  }
}

async function addNote({ text, reminder = null }) {
  const normalizedText = text.trim();
  if (!normalizedText) {
    showToast("Пустую заметку добавить нельзя.", "warning");
    return null;
  }

  const normalizedReminder = normalizeReminderValue(reminder);
  const note = {
    id: generateId(),
    text: normalizedText,
    reminder: normalizedReminder,
    createdAt: new Date().toISOString(),
    originClientId: getClientId(),
  };

  const notes = getStoredNotes();
  notes.push(note);
  saveNotes(notes);
  renderNotes();

  const subscription = await syncCurrentPushSubscription().catch((error) => {
    console.debug("Push subscription sync before note creation failed", error);
    return null;
  });

  sendRealtimeTask(note);

  if (normalizedReminder) {
    try {
      await scheduleReminder(note);
      if (supportsPushFeatures() && !subscription) {
        showToast("Напоминание запланировано. Чтобы получить системный push, включите уведомления.", "warning");
      } else {
        showToast("Напоминание запланировано.", "info");
      }
    } catch (error) {
      console.error("Failed to schedule reminder", error);
      showToast("Заметка сохранена, но сервер не запланировал напоминание.", "warning");
    }
    return note;
  }

  showToast("Заметка сохранена.", "info");
  return note;
}

function renderNotes() {
  const list = document.querySelector("#notes-list");
  const counter = document.querySelector("#notes-count");
  if (!list || !counter) {
    return;
  }

  const notes = getStoredNotes().sort((left, right) => {
    return new Date(right.createdAt).getTime() - new Date(left.createdAt).getTime();
  });

  list.innerHTML = "";
  counter.textContent = `${notes.length} ${pluralizeNotes(notes.length)}`;

  if (notes.length === 0) {
    return;
  }

  const fragment = document.createDocumentFragment();
  notes.forEach((note) => {
    const item = document.createElement("li");
    item.className = "note-item";

    const reminderMarkup = note.reminder
      ? `<p class="note-item__time note-item__time--accent">${escapeHTML(formatDisplayDate(note.reminder))}</p>`
      : "";

    item.innerHTML = `
      <p class="note-item__title"></p>
      <p class="note-item__time">${escapeHTML(formatDisplayDate(note.createdAt))}</p>
      ${reminderMarkup}
    `;

    const title = item.querySelector(".note-item__title");
    if (title) {
      title.textContent = note.text;
    }

    fragment.appendChild(item);
  });

  list.appendChild(fragment);
}

function getStoredNotes() {
  const raw = localStorage.getItem(NOTES_STORAGE_KEY);
  if (!raw) {
    return [];
  }

  try {
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) {
      return [];
    }

    let changed = false;
    const normalized = [];

    parsed.forEach((entry, index) => {
      const result = normalizeStoredNote(entry, index);
      if (!result) {
        changed = true;
        return;
      }

      normalized.push(result.note);
      if (result.changed) {
        changed = true;
      }
    });

    if (changed) {
      saveNotes(normalized);
    }

    return normalized;
  } catch (error) {
    console.warn("Broken localStorage notes payload. Resetting list.", error);
    return [];
  }
}

function normalizeStoredNote(entry, index) {
  if (typeof entry === "string") {
    const text = entry.trim();
    if (!text) {
      return null;
    }

    return {
      changed: true,
      note: {
        id: `legacy-${Date.now()}-${index}`,
        text,
        reminder: null,
        createdAt: new Date().toISOString(),
        originClientId: "",
      },
    };
  }

  if (!entry || typeof entry !== "object") {
    return null;
  }

  const text = typeof entry.text === "string" ? entry.text.trim() : "";
  if (!text) {
    return null;
  }

  const hasReminder = Object.prototype.hasOwnProperty.call(entry, "reminder");
  const hasLegacyDatetime = Object.prototype.hasOwnProperty.call(entry, "datetime");
  const normalizedReminder = normalizeReminderValue(hasReminder ? entry.reminder : entry.datetime);
  const normalizedCreatedAt = normalizeDateValue(entry.createdAt ?? entry.timestamp ?? entry.date);
  const normalizedID =
    typeof entry.id === "string" && entry.id.trim()
      ? entry.id
      : typeof entry.id === "number" && Number.isFinite(entry.id)
        ? String(entry.id)
        : `legacy-${Date.now()}-${index}`;
  const normalizedOrigin = typeof entry.originClientId === "string" ? entry.originClientId : "";

  const note = {
    id: normalizedID,
    text,
    reminder: normalizedReminder,
    createdAt: normalizedCreatedAt,
    originClientId: normalizedOrigin,
  };

  const changed =
    normalizedID !== entry.id ||
    text !== entry.text ||
    normalizedReminder !== (hasReminder ? entry.reminder : normalizeReminderValue(entry.datetime)) ||
    normalizedCreatedAt !== entry.createdAt ||
    normalizedOrigin !== entry.originClientId ||
    hasLegacyDatetime;

  return { note, changed };
}

function saveNotes(notes) {
  localStorage.setItem(NOTES_STORAGE_KEY, JSON.stringify(notes));
}

function handleStorageSync(event) {
  if (event.key !== NOTES_STORAGE_KEY) {
    return;
  }

  if (state.currentPage === "home") {
    renderNotes();
    showToast("Список заметок обновлен из другой вкладки.", "info");
  }
}

function handleServiceWorkerMessage(event) {
  const message = event.data;
  if (!message || typeof message !== "object") {
    return;
  }

  if (message.type === "push-received") {
    const payload = message.payload || {};
    console.info("Service worker received push payload", payload);

    if (document.visibilityState === "visible" && payload.reminderId && payload.title) {
      showToast(`Напоминание: ${payload.title}`, "info");
    }
    return;
  }

  if (message.type === "push-notification-failed") {
    console.error("Service worker failed to show notification", message.error);
    showToast(
      "Push получен, но браузер не показал системное уведомление. Проверьте разрешения сайта и уведомления Windows.",
      "warning",
    );
  }
}

async function initPushControls() {
  const toggle = document.querySelector("#push-toggle");
  if (!toggle) {
    return;
  }

  if (!toggle.dataset.bound) {
    toggle.dataset.bound = "true";
    toggle.addEventListener("click", async () => {
      if (toggle.disabled) {
        return;
      }

      if (toggle.dataset.pushState === "blocked") {
        showToast("Уведомления заблокированы в браузере.", "warning");
        return;
      }

      const registration = await registerServiceWorker();
      if (!registration) {
        await syncPushControls();
        return;
      }

      const subscription = await registration.pushManager.getSubscription();
      if (subscription) {
        await unsubscribeFromPush();
        return;
      }

      const permission = await ensurePushReadyFromUserGesture({ subscribeIfGranted: false });
      if (permission !== "granted") {
        await syncPushControls();
        return;
      }

      await subscribeToPush({ skipPermissionRequest: true });
    });
  }

  await syncPushControls();
}

async function syncCurrentPushSubscription() {
  if (!supportsPushFeatures()) {
    return null;
  }

  const registration = await registerServiceWorker();
  if (!registration) {
    return null;
  }

  if (await cleanupDeniedPushSubscription(registration)) {
    return null;
  }

  const subscription = await registration.pushManager.getSubscription();
  if (!subscription) {
    return null;
  }

  await ensureServerSubscription(subscription, true);
  return subscription;
}

async function syncPushControls() {
  const toggle = document.querySelector("#push-toggle");
  const indicator = document.querySelector("#push-indicator");
  if (!toggle || !indicator) {
    return;
  }

  if (!supportsPushFeatures()) {
    updatePushToggleState(toggle, indicator, "warning", "Push недоступен");
    return;
  }

  const config = await getAppConfig().catch(() => ({ pushAvailable: false }));
  if (!config.pushAvailable) {
    updatePushToggleState(toggle, indicator, "warning", "Push недоступен");
    return;
  }

  const registration = await registerServiceWorker();
  if (!registration) {
    updatePushToggleState(toggle, indicator, "warning", "Service Worker недоступен");
    return;
  }

  if (Notification.permission === "denied") {
    updatePushToggleState(toggle, indicator, "blocked", "Уведомления заблокированы");
    return;
  }

  await reconcileSubscriptionWithCurrentVAPIDKey(registration, config.vapidPublicKey);
  const subscription = await registration.pushManager.getSubscription();
  if (subscription) {
    try {
      await ensureServerSubscription(subscription);
    } catch (error) {
      console.error("Failed to sync existing subscription with server", error);
      updatePushToggleState(toggle, indicator, "warning", "Нет связи с push-сервером");
      return;
    }

    updatePushToggleState(toggle, indicator, "enabled", "Отключить уведомления");
    return;
  }

  state.subscriptionSyncedEndpoint = "";
  updatePushToggleState(
    toggle,
    indicator,
    Notification.permission === "default" ? "neutral" : "disabled",
    "Включить уведомления",
  );
}

function updatePushToggleState(toggle, indicator, mode, title) {
  toggle.dataset.pushState = mode;
  toggle.title = title;
  toggle.setAttribute("aria-label", title);
  toggle.disabled = mode === "warning";

  toggle.classList.remove(
    "push-toggle--neutral",
    "push-toggle--enabled",
    "push-toggle--disabled",
    "push-toggle--warning",
    "push-toggle--blocked",
  );
  indicator.classList.remove(
    "push-indicator--neutral",
    "push-indicator--enabled",
    "push-indicator--disabled",
    "push-indicator--warning",
    "push-indicator--blocked",
  );

  const toggleClass =
    mode === "enabled"
      ? "push-toggle--enabled"
      : mode === "disabled"
        ? "push-toggle--disabled"
        : mode === "warning"
          ? "push-toggle--warning"
          : mode === "blocked"
            ? "push-toggle--blocked"
            : "push-toggle--neutral";
  const indicatorClass =
    mode === "enabled"
      ? "push-indicator--enabled"
      : mode === "disabled"
        ? "push-indicator--disabled"
        : mode === "warning"
          ? "push-indicator--warning"
          : mode === "blocked"
            ? "push-indicator--blocked"
            : "push-indicator--neutral";

  toggle.classList.add(toggleClass);
  indicator.classList.add(indicatorClass);
}

async function ensurePushReadyFromUserGesture(options = {}) {
  if (!("Notification" in window)) {
    return "unsupported";
  }

  const { subscribeIfGranted = true } = options;
  let permission = Notification.permission;
  if (permission === "default") {
    permission = await Notification.requestPermission();
  }

  if (permission === "granted" && subscribeIfGranted) {
    const registration = await registerServiceWorker();
    if (registration) {
      const existing = await registration.pushManager.getSubscription();
      if (existing) {
        await ensureServerSubscription(existing, true);
      } else {
        await subscribeToPush({ skipPermissionRequest: true, silent: true });
      }
    }
  }

  return permission === "denied" ? "blocked" : permission;
}

async function subscribeToPush(options = {}) {
  try {
    const { skipPermissionRequest = false, silent = false } = options;

    let permission = Notification.permission;
    if (permission === "default" && !skipPermissionRequest) {
      permission = await ensurePushReadyFromUserGesture({ subscribeIfGranted: false });
    }

    const registration = await registerServiceWorker();
    const config = await getAppConfig();

    if (!registration || !config.pushAvailable || !config.vapidPublicKey) {
      showToast("Не удалось инициализировать push-уведомления.", "error");
      return;
    }

    if (Notification.permission === "denied") {
      showToast("Браузер заблокировал уведомления. Разрешите их в настройках сайта.", "warning");
      await syncPushControls();
      return;
    }

    if (permission !== "granted") {
      showToast("Разрешение на уведомления не выдано.", "warning");
      await syncPushControls();
      return;
    }

    const existing = await registration.pushManager.getSubscription();
    const subscription =
      existing ||
      (await registration.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: urlBase64ToUint8Array(config.vapidPublicKey),
      }));

    await ensureServerSubscription(subscription);
    rememberCurrentVAPIDPublicKey(config.vapidPublicKey);
    if (!silent) {
      showToast("Уведомления включены.", "info");
    }
    await syncPushControls();
  } catch (error) {
    console.error("Failed to subscribe to push", error);
    showToast("Не удалось включить уведомления.", "error");
  }
}

async function unsubscribeFromPush() {
  try {
    const registration = await registerServiceWorker();
    if (!registration) {
      return;
    }

    const subscription = await registration.pushManager.getSubscription();
    if (!subscription) {
      await syncPushControls();
      return;
    }

    const endpoint = subscription.endpoint;
    await subscription.unsubscribe();
    await postJSON("/api/push/unsubscribe", { endpoint });
    state.subscriptionSyncedEndpoint = "";
    localStorage.removeItem(VAPID_PUBLIC_KEY_STORAGE_KEY);
    showToast("Уведомления отключены.", "info");
    await syncPushControls();
  } catch (error) {
    console.error("Failed to unsubscribe from push", error);
    showToast("Не удалось отключить уведомления.", "error");
  }
}

async function cleanupDeniedPushSubscription(registration) {
  if (Notification.permission !== "denied" || !registration) {
    return false;
  }

  const subscription = await registration.pushManager.getSubscription();
  if (!subscription) {
    state.subscriptionSyncedEndpoint = "";
    localStorage.removeItem(VAPID_PUBLIC_KEY_STORAGE_KEY);
    return false;
  }

  const endpoint = subscription.endpoint;

  try {
    await subscription.unsubscribe();
  } catch (error) {
    console.debug("Failed to unsubscribe denied push subscription in browser", error);
  }

  try {
    await postJSON("/api/push/unsubscribe", { endpoint });
  } catch (error) {
    console.debug("Failed to unsubscribe denied push subscription on server", error);
  }

  state.subscriptionSyncedEndpoint = "";
  localStorage.removeItem(VAPID_PUBLIC_KEY_STORAGE_KEY);
  return true;
}

async function registerServiceWorker() {
  if (!("serviceWorker" in navigator)) {
    return null;
  }

  if (!state.registrationPromise) {
    state.registrationPromise = navigator.serviceWorker
      .register("/sw.js", { scope: "/" })
      .then(async (registration) => {
        await registration.update().catch(() => {});
        return navigator.serviceWorker.ready;
      })
      .catch((error) => {
        console.error("Failed to register service worker", error);
        showToast("Service Worker не удалось зарегистрировать.", "error");
        state.registrationPromise = null;
        return null;
      });
  }

  return state.registrationPromise;
}

async function getAppConfig() {
  if (!state.appConfigPromise) {
    state.appConfigPromise = fetch("/api/config", {
      headers: { Accept: "application/json" },
    })
      .then(async (response) => {
        if (!response.ok) {
          throw new Error(`HTTP ${response.status}`);
        }
        return response.json();
      })
      .catch((error) => {
        state.appConfigPromise = null;
        throw error;
      });
  }

  return state.appConfigPromise;
}

function initRealtime(forceReconnect = false) {
  if (!navigator.onLine) {
    updateConnectionStatus("Нет сети", "offline");
    return;
  }

  if (forceReconnect) {
    closeRealtimeSocket();
  }

  if (state.socket && state.socket.readyState === WebSocket.OPEN) {
    updateConnectionStatus("WebSocket подключен", "success");
    return;
  }

  if (state.socket && state.socket.readyState === WebSocket.CONNECTING) {
    updateConnectionStatus("Подключаем WebSocket...", "neutral");
    return;
  }

  void connectRealtime();
}

function closeRealtimeSocket() {
  if (!state.socket) {
    return;
  }

  const socket = state.socket;
  state.socket = null;
  state.socketToken += 1;

  try {
    socket.close(1000, "refresh");
  } catch (error) {
    console.debug("Socket close during reconnect failed", error);
  }
}

function isActiveSocket(socket, token) {
  return state.socket === socket && state.socketToken === token;
}

async function connectRealtime() {
  clearTimeout(state.reconnectTimer);
  state.reconnectTimer = null;

  if (!navigator.onLine) {
    updateConnectionStatus("Нет сети", "offline");
    return;
  }

  try {
    const config = await getAppConfig();
    const socketURL = new URL(config.wsPath || "/ws", window.location.origin);
    socketURL.protocol = socketURL.protocol === "https:" ? "wss:" : "ws:";

    const socket = new WebSocket(socketURL);
    const token = state.socketToken + 1;
    state.socketToken = token;
    state.socket = socket;
    updateConnectionStatus("Подключаем WebSocket...", "neutral");

    socket.addEventListener("open", () => {
      if (!isActiveSocket(socket, token)) {
        return;
      }

      state.reconnectDelay = 1000;
      updateConnectionStatus("WebSocket подключен", "success");
      flushPendingRealtimeTasks();
    });

    socket.addEventListener("message", (event) => {
      if (!isActiveSocket(socket, token)) {
        return;
      }

      const message = safeParse(event.data);
      if (!message || message.type !== "taskAdded" || !message.payload) {
        return;
      }

      if (message.payload.originClientId === getClientId()) {
        return;
      }

      showToast(`Новая заметка: ${message.payload.text}`, "info");
    });

    socket.addEventListener("close", () => {
      if (!isActiveSocket(socket, token)) {
        return;
      }

      state.socket = null;
      if (!navigator.onLine) {
        updateConnectionStatus("Нет сети", "offline");
        return;
      }

      updateConnectionStatus("WebSocket переподключается...", "warning");
      scheduleReconnect();
    });

    socket.addEventListener("error", (error) => {
      if (!isActiveSocket(socket, token)) {
        return;
      }

      console.debug("WebSocket error", error);
      if (socket.readyState !== WebSocket.OPEN) {
        updateConnectionStatus("Ошибка WebSocket", "warning");
      }
    });
  } catch (error) {
    console.error("Failed to initialize realtime websocket", error);
    state.socket = null;
    updateConnectionStatus(navigator.onLine ? "Realtime недоступен" : "Нет сети", navigator.onLine ? "warning" : "offline");
    scheduleReconnect();
  }
}

function sendRealtimeTask(note) {
  const payload = {
    type: "newTask",
    payload: {
      id: note.id,
      text: note.text,
      datetime: note.reminder ? new Date(note.reminder).toISOString() : "",
      createdAt: note.createdAt,
      originClientId: note.originClientId,
    },
  };

  if (!state.socket || state.socket.readyState !== WebSocket.OPEN) {
    state.pendingRealtimeTasks.push(payload);
    initRealtime();
    showToast(
      navigator.onLine
        ? "Заметка сохранена. Отправим событие после восстановления WebSocket."
        : "Заметка сохранена. WebSocket отправит событие после возвращения сети.",
      "warning",
    );
    return;
  }

  state.socket.send(JSON.stringify(payload));
}

function flushPendingRealtimeTasks() {
  if (!state.socket || state.socket.readyState !== WebSocket.OPEN || state.pendingRealtimeTasks.length === 0) {
    return;
  }

  while (state.pendingRealtimeTasks.length > 0) {
    const payload = state.pendingRealtimeTasks.shift();
    state.socket.send(JSON.stringify(payload));
  }
}

function scheduleReconnect() {
  clearTimeout(state.reconnectTimer);

  if (!navigator.onLine) {
    state.reconnectTimer = null;
    updateConnectionStatus("Нет сети", "offline");
    return;
  }

  const delay = state.reconnectDelay;
  state.reconnectTimer = window.setTimeout(() => {
    state.reconnectTimer = null;
    state.reconnectDelay = Math.min(state.reconnectDelay * 2, 15000);
    initRealtime();
  }, delay);
}

function updateConnectionStatus(text, tone) {
  const badge = document.querySelector("#connection-status");
  if (!badge) {
    return;
  }

  badge.textContent = text;
  badge.classList.remove("status-pill--neutral", "status-pill--success", "status-pill--warning", "status-pill--offline");
  badge.classList.add(
    tone === "success"
      ? "status-pill--success"
      : tone === "offline"
        ? "status-pill--offline"
        : tone === "warning"
          ? "status-pill--warning"
          : "status-pill--neutral",
  );
}

function showToast(message, kind = "info") {
  const region = document.querySelector("#toast-region");
  if (!region) {
    return;
  }

  const toast = document.createElement("div");
  toast.className = `toast toast--${kind}`;
  toast.textContent = message;
  region.appendChild(toast);

  window.setTimeout(() => {
    toast.remove();
  }, TOAST_DURATION);
}

async function syncFutureRemindersWithServer() {
  if (state.reminderSyncPromise) {
    return state.reminderSyncPromise;
  }

  state.reminderSyncPromise = (async () => {
    const futureNotes = getStoredNotes().filter((note) => note.reminder && note.reminder > Date.now());
    if (futureNotes.length === 0) {
      return;
    }

    const results = await Promise.allSettled(futureNotes.map((note) => scheduleReminder(note)));
    const failed = results.filter((result) => result.status === "rejected").length;
    if (failed > 0) {
      console.warn(`Failed to sync ${failed} reminder(s) with the server.`);
    }
  })();

  try {
    await state.reminderSyncPromise;
  } finally {
    state.reminderSyncPromise = null;
  }
}

async function scheduleReminder(note) {
  if (!note.reminder) {
    return null;
  }

  return postJSON("/api/reminders", {
    id: note.id,
    text: note.text,
    reminderTime: note.reminder,
  });
}

function generateId() {
  if (window.crypto && "randomUUID" in window.crypto) {
    return window.crypto.randomUUID();
  }
  return `note-${Date.now()}-${Math.floor(Math.random() * 100000)}`;
}

function getClientId() {
  let clientId = localStorage.getItem(CLIENT_ID_STORAGE_KEY);
  if (!clientId) {
    clientId = generateId();
    localStorage.setItem(CLIENT_ID_STORAGE_KEY, clientId);
  }
  return clientId;
}

function pluralizeNotes(count) {
  if (count % 10 === 1 && count % 100 !== 11) {
    return "заметка";
  }
  if ([2, 3, 4].includes(count % 10) && ![12, 13, 14].includes(count % 100)) {
    return "заметки";
  }
  return "заметок";
}

function formatDisplayDate(value) {
  if (!value) {
    return "—";
  }

  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) {
    return String(value);
  }

  return new Intl.DateTimeFormat("ru-RU", {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(date);
}

function parseReminderInput(value) {
  if (!value) {
    return null;
  }

  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return null;
  }

  return date.getTime();
}

function normalizeReminderValue(value) {
  if (value === null || value === undefined || value === "") {
    return null;
  }

  if (typeof value === "number" && Number.isFinite(value)) {
    return Math.trunc(value);
  }

  if (typeof value === "string") {
    const trimmed = value.trim();
    if (!trimmed) {
      return null;
    }

    if (/^\d+$/.test(trimmed)) {
      const asNumber = Number(trimmed);
      return Number.isFinite(asNumber) ? Math.trunc(asNumber) : null;
    }

    const parsedDate = new Date(trimmed);
    return Number.isNaN(parsedDate.getTime()) ? null : parsedDate.getTime();
  }

  return null;
}

function normalizeDateValue(value) {
  if (typeof value === "string") {
    const parsed = new Date(value);
    if (!Number.isNaN(parsed.getTime())) {
      return parsed.toISOString();
    }
  }

  if (typeof value === "number" && Number.isFinite(value)) {
    const parsed = new Date(value);
    if (!Number.isNaN(parsed.getTime())) {
      return parsed.toISOString();
    }
  }

  return new Date().toISOString();
}

function escapeHTML(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

function safeParse(raw) {
  try {
    return JSON.parse(raw);
  } catch (error) {
    console.debug("Failed to parse JSON payload", error);
    return null;
  }
}

async function postJSON(url, payload) {
  const response = await fetch(url, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Accept: "application/json",
    },
    body: JSON.stringify(payload),
  });

  if (!response.ok) {
    const message = await response.text().catch(() => "");
    throw new Error(message || `HTTP ${response.status}`);
  }

  return response.json();
}

async function ensureServerSubscription(subscription, force = false) {
  const endpoint = subscription?.endpoint;
  if (!endpoint) {
    throw new Error("Subscription endpoint is missing");
  }
  if (!force && state.subscriptionSyncedEndpoint === endpoint) {
    return;
  }

  await postJSON("/api/push/subscribe", subscription.toJSON());
  state.subscriptionSyncedEndpoint = endpoint;
}

async function reconcileSubscriptionWithCurrentVAPIDKey(registration, currentPublicKey) {
  const subscription = await registration.pushManager.getSubscription();
  if (!subscription || !currentPublicKey) {
    return;
  }

  const rememberedKey = localStorage.getItem(VAPID_PUBLIC_KEY_STORAGE_KEY);
  if (!rememberedKey || rememberedKey === currentPublicKey) {
    return;
  }

  await subscription.unsubscribe();
  state.subscriptionSyncedEndpoint = "";
  localStorage.removeItem(VAPID_PUBLIC_KEY_STORAGE_KEY);
  showToast("Ключ push-уведомлений изменился. Подпишитесь заново.", "warning");
}

function rememberCurrentVAPIDPublicKey(publicKey) {
  if (!publicKey) {
    return;
  }
  localStorage.setItem(VAPID_PUBLIC_KEY_STORAGE_KEY, publicKey);
}

function urlBase64ToUint8Array(base64String) {
  const padding = "=".repeat((4 - (base64String.length % 4)) % 4);
  const base64 = (base64String + padding).replace(/-/g, "+").replace(/_/g, "/");
  const raw = window.atob(base64);
  const output = new Uint8Array(raw.length);

  for (let index = 0; index < raw.length; index += 1) {
    output[index] = raw.charCodeAt(index);
  }

  return output;
}

function supportsPushFeatures() {
  return "serviceWorker" in navigator && "PushManager" in window && "Notification" in window;
}
