(function () {
  "use strict";

  // Client-side confirmation for delete forms. The server does not trust
  // this check; it only makes the destructive action harder to trigger
  // accidentally. Deletes are idempotent and remain safe without JS.
  document.querySelectorAll("form[data-confirm]").forEach(function (form) {
    form.addEventListener("submit", function (event) {
      var message = form.getAttribute("data-confirm") || "Are you sure?";
      if (!window.confirm(message)) {
        event.preventDefault();
      }
    });
  });

  // Quick status updates. Interacts with the server-side form endpoint, so
  // it still works (progressively) when JavaScript is disabled.
  document.querySelectorAll("select[data-task-status]").forEach(function (select) {
    select.addEventListener("change", function () {
      var taskID = select.getAttribute("data-task-status");
      var form = document.createElement("form");
      form.method = "post";
      form.action = "/tasks/" + taskID + "/status";
      var statusInput = document.createElement("input");
      statusInput.type = "hidden";
      statusInput.name = "status";
      statusInput.value = select.value;
      form.appendChild(statusInput);
      document.body.appendChild(form);
      form.submit();
    });
  });
})();