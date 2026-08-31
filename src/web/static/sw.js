/* Service worker: offline support for the panel.

   Guaranteed-Response rule — every branch of every fetch handler must end in a
   Response object. A handler that throws, or that resolves to undefined, turns
   a working page into a browser network error, so each path here has an
   explicit fallback and the last fallback is synthesised locally. */

var CACHE_NAME = "cashp-static-v1";

var PRECACHE_ASSETS = [
  "/offline.html",
  "/static/css/common.css",
  "/static/css/components.css",
  "/static/css/public.css",
  "/static/css/print.css",
  "/static/js/app.js",
  "/static/icons/icon.svg",
  "/manifest.json"
];

var OFFLINE_URL = "/offline.html";

function offlineFallback() {
  return caches.match(OFFLINE_URL).then(function (cached) {
    if (cached) {
      return cached;
    }
    return new Response(
      "You are offline and the offline page is not cached yet. Reconnect and reload.",
      {
        status: 503,
        statusText: "Service Unavailable",
        headers: { "Content-Type": "text/plain; charset=utf-8" }
      }
    );
  });
}

function emptyFallback() {
  return new Response("", {
    status: 503,
    statusText: "Service Unavailable",
    headers: { "Content-Type": "text/plain; charset=utf-8" }
  });
}

self.addEventListener("install", function (event) {
  event.waitUntil(
    caches
      .open(CACHE_NAME)
      .then(function (cache) {
        return cache.addAll(PRECACHE_ASSETS);
      })
      .catch(function () {
        /* A missing asset must not abort the install: the worker still serves
           everything it did manage to cache. */
      })
  );
});

self.addEventListener("activate", function (event) {
  event.waitUntil(
    caches
      .keys()
      .then(function (names) {
        return Promise.all(
          names.map(function (name) {
            if (name !== CACHE_NAME) {
              return caches.delete(name);
            }
            return Promise.resolve(false);
          })
        );
      })
      .then(function () {
        return self.clients.claim();
      })
  );
});

/* Network-first for pages: a control panel must never show stale state, so the
   cache is only consulted once the network has actually failed. */
function handleNavigation(request) {
  return fetch(request)
    .then(function (response) {
      return response;
    })
    .catch(function () {
      return caches.match(request).then(function (cached) {
        return cached || offlineFallback();
      });
    });
}

/* Cache-first for static assets: they are versioned by CACHE_NAME and change
   only when the application is upgraded. */
function handleStatic(request) {
  return caches.match(request).then(function (cached) {
    if (cached) {
      return cached;
    }
    return fetch(request)
      .then(function (response) {
        if (response && response.ok) {
          var copy = response.clone();
          caches
            .open(CACHE_NAME)
            .then(function (cache) {
              return cache.put(request, copy);
            })
            .catch(function () {
              /* Storage pressure is not fatal — the response is already on its
                 way to the page. */
            });
        }
        return response;
      })
      .catch(function () {
        return emptyFallback();
      });
  });
}

self.addEventListener("fetch", function (event) {
  var request = event.request;
  if (request.method !== "GET") {
    return;
  }

  var url = new URL(request.url);
  if (url.origin !== self.location.origin) {
    return;
  }

  /* API traffic is never intercepted: a cached API answer would show a state
     the server no longer has. */
  if (url.pathname.indexOf("/api/") === 0) {
    return;
  }

  if (request.mode === "navigate") {
    event.respondWith(handleNavigation(request));
    return;
  }

  if (url.pathname.indexOf("/static/") === 0 || url.pathname === "/manifest.json") {
    event.respondWith(handleStatic(request));
  }
});

self.addEventListener("message", function (event) {
  if (event.data === "SKIP_WAITING") {
    self.skipWaiting();
  }
});
