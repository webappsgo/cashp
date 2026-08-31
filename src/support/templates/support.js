// Support progressive enhancement.
//
// Nothing here is required: the ticket form submits, the draft saves and the
// chat updates with this file blocked or absent. It adds only the two things
// HTML and CSS cannot express on their own — a timed background save and a
// timed background refresh of a conversation.
(function () {
  "use strict";

  // autosave posts the ticket draft on a fixed interval so a long description
  // survives a closed laptop. The form still has its own Save draft button.
  function autosave() {
    var form = document.getElementById("support-ticket-form");
    if (!form) {
      return;
    }
    var url = form.getAttribute("data-draft-url");
    var seconds = parseInt(form.getAttribute("data-autosave-seconds"), 10);
    var status = document.getElementById("support-autosave-status");
    if (!url || !seconds || seconds < 5) {
      return;
    }
    window.setInterval(function () {
      var body = new FormData(form);
      window
        .fetch(url, {
          method: "POST",
          body: body,
          credentials: "same-origin",
          headers: { Accept: "application/json" },
        })
        .then(function (response) {
          if (status) {
            status.textContent = response.ok
              ? "Draft saved."
              : "Draft could not be saved just now — use Save draft.";
          }
        })
        .catch(function () {
          if (status) {
            status.textContent = "Draft could not be saved just now — use Save draft.";
          }
        });
    }, seconds * 1000);
  }

  // refreshChat replaces the message log with the server's current version.
  // The markup it inserts is this application's own escaped output, fetched
  // from the same origin as the page it is replacing.
  function refreshChat() {
    var log = document.getElementById("support-chat-log");
    if (!log) {
      return;
    }
    window.setInterval(function () {
      window
        .fetch(window.location.href, {
          credentials: "same-origin",
          headers: { Accept: "text/html" },
        })
        .then(function (response) {
          return response.ok ? response.text() : null;
        })
        .then(function (text) {
          if (!text) {
            return;
          }
          var parsed = new DOMParser().parseFromString(text, "text/html");
          var fresh = parsed.getElementById("support-chat-log");
          if (fresh) {
            log.replaceChildren.apply(log, Array.prototype.slice.call(fresh.childNodes));
            log.scrollTop = log.scrollHeight;
          }
        })
        .catch(function () {
          // A failed refresh needs no report: the Refresh link is still there
          // and the next tick tries again.
        });
    }, 10000);
  }

  autosave();
  refreshChat();
})();
