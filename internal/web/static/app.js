/*
 * app.js - drag-and-drop file selection and upload progress for filetwist.
 * Everything else in the interface is server-rendered HTML swapped by htmx.
 */
(function () {
  "use strict";

  var notice = document.getElementById("request-notice");

  function showRequestError(request) {
    var message = "The request failed. Please try again.";
    if (request && request.getResponseHeader("Content-Type") &&
        request.getResponseHeader("Content-Type").indexOf("text/html") !== -1) {
      var parsed = new DOMParser().parseFromString(request.responseText, "text/html");
      var alert = parsed.querySelector("p[role='alert']");
      if (alert) {
        message = alert.textContent;
      }
    }
    var paragraph = document.createElement("p");
    paragraph.className = "error";
    paragraph.setAttribute("role", "alert");
    paragraph.textContent = message;
    notice.replaceChildren(paragraph);
  }

  document.body.addEventListener("htmx:beforeSwap", function (event) {
    if (event.detail.xhr.status >= 400) {
      // Keep selections, downloads, and retry controls on a failed action.
      event.detail.shouldSwap = false;
      showRequestError(event.detail.xhr);
    } else if (event.detail.requestConfig && event.detail.requestConfig.verb !== "get") {
      notice.replaceChildren();
    }
  });
  document.body.addEventListener("htmx:sendError", function () {
    showRequestError();
  });

  var form = document.getElementById("upload-form");
  if (!form) {
    return;
  }

  var input = document.getElementById("file-input");
  var dropZone = document.getElementById("drop-zone");
  var fileList = document.getElementById("file-list");
  var progressWrap = document.getElementById("upload-progress");
  var progressBar = document.getElementById("upload-progress-bar");
  var progressText = document.getElementById("upload-progress-text");
  var panel = document.getElementById("job-panel");
  var submit = document.getElementById("upload-submit");
  var uploading = false;

  function describeFiles() {
    var files = input.files;
    if (!files || files.length === 0) {
      fileList.textContent = "No files selected.";
      submit.disabled = true;
      return;
    }
    var total = 0;
    var names = [];
    for (var index = 0; index < files.length; index++) {
      total += files[index].size;
      names.push(files[index].name);
    }
    fileList.textContent =
      files.length + (files.length === 1 ? " file" : " files") +
      " selected (" + formatBytes(total) + "): " + names.join(", ");
    submit.disabled = uploading;
  }

  function formatBytes(value) {
    var units = ["B", "KiB", "MiB", "GiB"];
    var size = value;
    var unit = 0;
    while (size >= 1024 && unit < units.length - 1) {
      size /= 1024;
      unit++;
    }
    return (unit === 0 ? size : size.toFixed(1)) + " " + units[unit];
  }

  input.addEventListener("change", describeFiles);

  ["dragenter", "dragover"].forEach(function (name) {
    dropZone.addEventListener(name, function (event) {
      event.preventDefault();
      dropZone.classList.add("drop-zone--active");
    });
  });

  ["dragleave", "drop"].forEach(function (name) {
    dropZone.addEventListener(name, function (event) {
      event.preventDefault();
      dropZone.classList.remove("drop-zone--active");
    });
  });

  dropZone.addEventListener("drop", function (event) {
    if (uploading || !event.dataTransfer || !event.dataTransfer.files.length) {
      return;
    }
    input.files = event.dataTransfer.files;
    describeFiles();
  });

  dropZone.addEventListener("click", function () {
    input.click();
  });

  dropZone.addEventListener("keydown", function (event) {
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      input.click();
    }
  });

  form.addEventListener("submit", function (event) {
    event.preventDefault();
    if (uploading || !input.files || input.files.length === 0) {
      return;
    }

    var data = new FormData();
    for (var index = 0; index < input.files.length; index++) {
      data.append("files", input.files[index]);
    }

    var request = new XMLHttpRequest();
    request.open("POST", form.getAttribute("action"), true);
    request.setRequestHeader("HX-Request", "true");
    uploading = true;
    input.disabled = true;
    notice.replaceChildren();
    submit.disabled = true;
    progressWrap.hidden = false;
    progressBar.value = 0;
    progressText.textContent = "Uploading...";

    request.upload.addEventListener("progress", function (progress) {
      if (!progress.lengthComputable) {
        return;
      }
      var percent = Math.round((progress.loaded / progress.total) * 100);
      progressBar.value = percent;
      progressText.textContent = "Uploading " + percent + "%";
    });

    request.addEventListener("load", function () {
      uploading = false;
      input.disabled = false;
      progressText.textContent = request.status < 400 ? "Upload complete." : "Upload rejected.";
      progressWrap.hidden = request.status < 400;
      if (request.status < 400) {
        panel.innerHTML = request.responseText;
        if (window.htmx) {
          window.htmx.process(panel);
        }
        form.reset();
        describeFiles();
      } else {
        showRequestError(request);
        describeFiles();
      }
    });

    request.addEventListener("error", function () {
      uploading = false;
      input.disabled = false;
      progressText.textContent = "Upload failed.";
      showRequestError();
      describeFiles();
    });

    request.send(data);
  });

  describeFiles();
})();
