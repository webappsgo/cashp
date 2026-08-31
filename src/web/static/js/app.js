/* Progressive enhancement only. Every feature on this site already works
   without this file: forms post and redirect, dialogs render inline, and the
   theme is chosen server-side. Nothing here carries business logic. */

(function () {
  "use strict";

  var TOAST_TIMEOUT = 5000;
  var COPIED_TIMEOUT = 2000;
  var THEMES = ["dark", "light", "auto"];

  /* The no-js class gates the fallbacks that only apply when this file never
     runs, so removing it is the first thing done. */
  document.documentElement.classList.remove("no-js");

  function toastRegion() {
    return document.getElementById("toast-region");
  }

  /* Toasts are additive feedback. Anything a user must not miss is also
     rendered server-side as a flash message. */
  function toast(message, level) {
    var region = toastRegion();
    if (!region) {
      return;
    }
    var node = document.createElement("div");
    node.className = "toast toast-" + (level || "info");
    node.textContent = message;
    region.appendChild(node);
    window.setTimeout(function () {
      node.remove();
    }, TOAST_TIMEOUT);
  }

  function copyText(text) {
    if (navigator.clipboard && window.isSecureContext) {
      return navigator.clipboard.writeText(text);
    }
    return Promise.reject(new Error("clipboard unavailable"));
  }

  /* Copy buttons: visible "Copied!" confirmation on the button itself, plus a
     toast, so the result is obvious with or without a screen reader. */
  function initCopyButtons() {
    document.addEventListener("click", function (event) {
      var button = event.target.closest("[data-copy]");
      if (!button) {
        return;
      }
      event.preventDefault();
      var label = button.querySelector(".copy-text");
      var original = label ? label.textContent : "";
      copyText(button.getAttribute("data-copy")).then(
        function () {
          button.classList.add("copied");
          if (label) {
            label.textContent = button.getAttribute("data-copied-label") || "Copied!";
          }
          window.setTimeout(function () {
            button.classList.remove("copied");
            if (label) {
              label.textContent = original;
            }
          }, COPIED_TIMEOUT);
        },
        function () {
          toast("Copying failed — select the text and copy it manually.", "error");
        }
      );
    });
  }

  /* A button carrying data-confirm opens the matching <dialog> instead of
     submitting straight away. With this file absent the dialog is already on
     the page as an inline form, so the action stays reachable. */
  function initConfirmDialogs() {
    document.addEventListener("click", function (event) {
      var trigger = event.target.closest("[data-confirm]");
      if (!trigger) {
        return;
      }
      var dialog = document.getElementById(trigger.getAttribute("data-confirm"));
      if (!dialog || typeof dialog.showModal !== "function") {
        return;
      }
      event.preventDefault();
      dialog.showModal();
    });
  }

  function initReloadButtons() {
    document.addEventListener("click", function (event) {
      var button = event.target.closest('[data-action="reload"]');
      if (!button) {
        return;
      }
      event.preventDefault();
      window.location.reload();
    });
  }

  function readCookie(name) {
    var parts = document.cookie ? document.cookie.split("; ") : [];
    for (var i = 0; i < parts.length; i += 1) {
      var pair = parts[i].split("=");
      if (pair[0] === name) {
        return decodeURIComponent(pair.slice(1).join("="));
      }
    }
    return "";
  }

  function writeCookie(name, value) {
    var secure = window.location.protocol === "https:" ? "; Secure" : "";
    document.cookie =
      name + "=" + encodeURIComponent(value) + "; Path=/; Max-Age=31536000; SameSite=Lax" + secure;
  }

  /* Theme switch without a round trip. The cookie is still the source of
     truth so the server renders the same theme on the next request, which is
     why the value is written here rather than into localStorage. */
  function initThemeToggle() {
    var form = document.querySelector(".theme-toggle");
    if (!form) {
      return;
    }
    form.addEventListener("click", function (event) {
      var button = event.target.closest("button[name='theme']");
      if (!button) {
        return;
      }
      var theme = button.value;
      if (THEMES.indexOf(theme) === -1) {
        return;
      }
      event.preventDefault();
      writeCookie("theme", theme);
      var root = document.documentElement;
      root.classList.remove("theme-dark", "theme-light", "theme-auto");
      root.classList.add("theme-" + theme);
      root.setAttribute("data-theme", theme);
      form.querySelectorAll("button[name='theme']").forEach(function (candidate) {
        var active = candidate.value === theme;
        candidate.classList.toggle("theme-btn-active", active);
        candidate.setAttribute("aria-pressed", active ? "true" : "false");
      });
      toast("Theme set to " + theme + ".", "success");
    });
  }

  /* The consent banner is dismissed in place; the same POST still runs so the
     server stores the decision. */
  function initCookieBanner() {
    var banner = document.getElementById("cookie-consent");
    if (!banner) {
      return;
    }
    banner.addEventListener("submit", function (event) {
      var form = event.target;
      if (!(form instanceof HTMLFormElement)) {
        return;
      }
      event.preventDefault();
      var body = new URLSearchParams(new FormData(form));
      window
        .fetch(form.action, {
          method: "POST",
          body: body,
          credentials: "same-origin",
          headers: { "X-Requested-With": "fetch" }
        })
        .then(function (response) {
          if (!response.ok) {
            throw new Error("consent request failed");
          }
          banner.remove();
          toast("Your cookie choice has been saved.", "success");
        })
        .catch(function () {
          form.submit();
        });
    });
  }

  /* Closing the mobile navigation with Escape matches the behaviour people
     expect from an overlay; the checkbox itself keeps working without JS. */
  function initNavEscape() {
    var toggle = document.getElementById("nav-toggle");
    if (!toggle) {
      return;
    }
    document.addEventListener("keydown", function (event) {
      if (event.key === "Escape" && toggle.checked) {
        toggle.checked = false;
        toggle.focus();
      }
    });
  }

  function initConnectionNotices() {
    window.addEventListener("offline", function () {
      toast("You are offline. Changes cannot be saved until the connection returns.", "error");
    });
    window.addEventListener("online", function () {
      toast("Connection restored.", "success");
    });
  }

  /* The service worker only caches static assets and serves the offline page;
     it never changes what a request means. */
  function initServiceWorker() {
    if (!("serviceWorker" in navigator)) {
      return;
    }
    navigator.serviceWorker.register("/sw.js").then(function (registration) {
      registration.addEventListener("updatefound", function () {
        var installing = registration.installing;
        if (!installing) {
          return;
        }
        installing.addEventListener("statechange", function () {
          if (installing.state === "installed" && navigator.serviceWorker.controller) {
            toast("A new version is available — reload to update.", "info");
          }
        });
      });
    }, function () {
      /* Registration failing is not an error the visitor can act on: the site
         works unchanged, only offline support is missing. */
    });
  }

  initCopyButtons();
  initConfirmDialogs();
  initReloadButtons();
  initThemeToggle();
  initCookieBanner();
  initNavEscape();
  initConnectionNotices();
  initServiceWorker();

  /* Read once so a stale value written by an older version cannot linger
     unnoticed: an unknown theme is rewritten to the rendered one. */
  var storedTheme = readCookie("theme");
  if (storedTheme && THEMES.indexOf(storedTheme) === -1) {
    writeCookie("theme", document.documentElement.getAttribute("data-theme") || "dark");
  }
})();
