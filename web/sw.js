const APP_SHELL_CACHE = "app-shell-v10";
const DYNAMIC_CACHE = "dynamic-content-v10";
const HOME_FALLBACK = "/content/home.html";

const APP_SHELL_ASSETS = [
  "/",
  "/index.html",
  "/styles.css",
  "/app.js",
  "/sw.js",
  "/manifest.json",
  "/content/home.html",
  "/icons/favicon.ico",
  "/icons/favicon-16x16.png",
  "/icons/favicon-32x32.png",
  "/icons/favicon-48x48.png",
  "/icons/favicon-64x64.png",
  "/icons/favicon-128x128.png",
  "/icons/favicon-152x152.png",
  "/icons/icon-192.png",
  "/icons/favicon-256x256.png",
  "/icons/favicon-512x512.png",
  "/icons/icon-maskable-512.png",
];

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches
      .open(APP_SHELL_CACHE)
      .then((cache) => cache.addAll(APP_SHELL_ASSETS))
      .then(() => self.skipWaiting()),
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) =>
        Promise.all(keys.filter((key) => ![APP_SHELL_CACHE, DYNAMIC_CACHE].includes(key)).map((key) => caches.delete(key))),
      )
      .then(() => self.clients.claim()),
  );
});

self.addEventListener("fetch", (event) => {
  const { request } = event;
  if (request.method !== "GET") {
    return;
  }

  const url = new URL(request.url);
  if (url.origin !== self.location.origin) {
    return;
  }
  if (url.pathname.startsWith("/api/") || url.pathname === "/ws") {
    return;
  }

  if (url.pathname.startsWith("/content/")) {
    event.respondWith(networkFirst(request));
    return;
  }

  if (isAppShellRequest(url.pathname)) {
    event.respondWith(cacheFirst(request));
  }
});

self.addEventListener("push", (event) => {
  const fallback = {
    title: "Новая задача",
    body: "В приложении появилась новая задача.",
    url: "/",
    reminderId: null,
  };

  let payload = fallback;
  if (event.data) {
    try {
      payload = { ...fallback, ...event.data.json() };
    } catch (error) {
      payload = { ...fallback, body: event.data.text() || fallback.body };
    }
  }

  const options = {
    body: payload.body,
    icon: "/icons/icon-192.png",
    badge: "/icons/favicon-64x64.png",
    data: {
      url: payload.url || "/",
      reminderId: payload.reminderId || null,
    },
  };

  if (payload.reminderId) {
    options.actions = [{ action: "snooze", title: "Отложить на 5 минут" }];
  }

  event.waitUntil(self.registration.showNotification(payload.title, options));
});

self.addEventListener("notificationclick", (event) => {
  event.waitUntil(handleNotificationClick(event));
});

self.addEventListener("pushsubscriptionchange", (event) => {
  event.waitUntil(handlePushSubscriptionChange(event));
});

function isAppShellRequest(pathname) {
  return pathname === "/" || APP_SHELL_ASSETS.includes(pathname) || pathname.startsWith("/icons/");
}

async function cacheFirst(request) {
  const cached = await caches.match(request);
  if (cached) {
    return cached;
  }

  const response = await fetch(request);
  const cache = await caches.open(APP_SHELL_CACHE);
  cache.put(request, response.clone());
  return response;
}

async function networkFirst(request) {
  const cache = await caches.open(DYNAMIC_CACHE);

  try {
    const response = await fetch(request);
    cache.put(request, response.clone());
    return response;
  } catch (error) {
    const cached = await cache.match(request);
    if (cached) {
      return cached;
    }

    const fallback = await caches.match(HOME_FALLBACK);
    if (fallback) {
      return fallback;
    }

    return new Response("<h1>Offline</h1><p>Контент недоступен без подключения.</p>", {
      headers: { "Content-Type": "text/html; charset=utf-8" },
      status: 503,
    });
  }
}

async function handleNotificationClick(event) {
  const targetURL = event.notification.data?.url || "/";
  const reminderId = event.notification.data?.reminderId || "";

  if (event.action === "snooze" && reminderId) {
    try {
      await fetch("/api/reminders/snooze", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Accept: "application/json",
        },
        body: JSON.stringify({ reminderId }),
      });
    } catch (error) {
      console.error("Failed to snooze reminder", error);
    }

    event.notification.close();
    return;
  }

  event.notification.close();

  const clients = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
  for (const client of clients) {
    if ("focus" in client) {
      await client.focus();
      return;
    }
  }

  if (self.clients.openWindow) {
    await self.clients.openWindow(targetURL);
  }
}

async function handlePushSubscriptionChange(event) {
  try {
    if (event.oldSubscription?.endpoint) {
      await fetch("/api/push/unsubscribe", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Accept: "application/json",
        },
        body: JSON.stringify({ endpoint: event.oldSubscription.endpoint }),
      }).catch(() => {});
    }

    const configResponse = await fetch("/api/config", {
      headers: { Accept: "application/json" },
    });
    if (!configResponse.ok) {
      throw new Error(`HTTP ${configResponse.status}`);
    }

    const config = await configResponse.json();
    if (!config.pushAvailable || !config.vapidPublicKey) {
      return;
    }

    const subscription = await self.registration.pushManager.subscribe({
      userVisibleOnly: true,
      applicationServerKey: urlBase64ToUint8Array(config.vapidPublicKey),
    });

    await fetch("/api/push/subscribe", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Accept: "application/json",
      },
      body: JSON.stringify(subscription.toJSON()),
    });
  } catch (error) {
    console.error("Failed to refresh push subscription", error);
  }
}

function urlBase64ToUint8Array(base64String) {
  const padding = "=".repeat((4 - (base64String.length % 4)) % 4);
  const base64 = (base64String + padding).replace(/-/g, "+").replace(/_/g, "/");
  const raw = atob(base64);
  const output = new Uint8Array(raw.length);

  for (let index = 0; index < raw.length; index += 1) {
    output[index] = raw.charCodeAt(index);
  }

  return output;
}
