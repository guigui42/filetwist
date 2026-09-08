/*
 * Progressive controls for file selection, preset review, and job navigation.
 * Conversion state and results remain server-rendered HTML swapped by htmx.
 */
(function () {
  "use strict";

  var notice = document.getElementById("request-notice");
  var uploadSection = document.getElementById("upload-section");
  var swapState = null;
  var requestSources = new WeakMap();

  function focusElement(element) {
    if (element) {
      element.focus({ preventScroll: true });
      element.scrollIntoView({ block: "nearest" });
    }
  }

  function noticeFor(source) {
    var job = source && source.closest("#job");
    if (job) {
      return job.querySelector(".request-notice");
    }
    var upload = source && source.closest("#upload-section");
    return upload ? document.getElementById("upload-notice") : notice;
  }

  function showNotice(message, target, focus) {
    if (target.textContent !== message) {
      var paragraph = document.createElement("p");
      paragraph.className = "error";
      paragraph.setAttribute("role", "alert");
      paragraph.textContent = message;
      target.replaceChildren(paragraph);
    }
    if (focus) {
      focusElement(target);
    }
  }

  function showStatus(message, target) {
    if (target.textContent !== message) {
      var paragraph = document.createElement("p");
      paragraph.className = "hint";
      paragraph.setAttribute("role", "status");
      paragraph.textContent = message;
      target.replaceChildren(paragraph);
    }
  }

  function clearNotices() {
    document.querySelectorAll(".request-notice").forEach(function (target) {
      target.replaceChildren();
    });
  }

  function showRequestError(request, source, focus) {
    var message = "The request failed. Please try again.";
    if (request && request.getResponseHeader("Content-Type") &&
        request.getResponseHeader("Content-Type").indexOf("text/html") !== -1) {
      var parsed = new DOMParser().parseFromString(request.responseText, "text/html");
      var alert = parsed.querySelector("p[role='alert']");
      if (alert) {
        message = alert.textContent;
      }
    }
    showNotice(message, noticeFor(source), focus);
  }

  function updateOperationHelp(select) {
    var selected = select.selectedOptions[0];
    var help = document.getElementById(select.getAttribute("aria-describedby"));
    if (help) {
      help.textContent = selected.dataset.description;
    }
    var workOrder = select.closest(".work-order");
    var outputFormat = workOrder && workOrder.querySelector("[data-output-format]");
    if (outputFormat) {
      outputFormat.textContent = selected.dataset.format;
    }
  }

  function enableBatchDownloads(job) {
    var button = job && job.querySelector("[data-download-all]");
    if (button) {
      button.hidden = false;
    }
  }

  function startBatchDownloads(button) {
    var links = Array.from(button.closest("#job").querySelectorAll("[data-download-file]"));
    var target = noticeFor(button);
    var label = button.textContent;
    var index = 0;
    button.disabled = true;
    button.textContent = "Starting...";

    function startNext() {
      if (index < links.length) {
        showStatus("Starting download " + (index + 1) + " of " + links.length + "...", target);
        links[index].click();
        index++;
        window.setTimeout(startNext, 250);
        return;
      }
      button.disabled = false;
      button.textContent = label;
      var message = "Started " + links.length + " download" + plural(links.length) + ".";
      if (links.length > 1) {
        message += " If your browser asks, allow multiple downloads for this site.";
      }
      showStatus(message, target);
    }

    startNext();
  }

  function rememberJob(preserveSelections) {
    var job = document.getElementById("job");
    if (!job) {
      return null;
    }
    var selections = {};
    if (preserveSelections) {
      job.querySelectorAll("[data-operation]").forEach(function (select) {
        selections[select.id] = { value: select.value, overridden: select.dataset.overridden || "" };
      });
    }
    return {
      id: job.dataset.jobId,
      state: job.dataset.jobState,
      focusedID: document.activeElement.id,
      hadFocus: job.contains(document.activeElement),
      details: Array.from(job.querySelectorAll("details[open]")).map(function (item) { return item.id; }),
      selections: selections,
      batchValue: job.querySelector("#batch-operation") ? job.querySelector("#batch-operation").value : "",
      keepIndividual: !job.querySelector("#keep-individual") || job.querySelector("#keep-individual").checked
    };
  }

  function syncJob(focus, previous) {
    var job = document.getElementById("job");
    if (uploadSection) {
      if (!job || focus) {
        uploadSection.open = !job;
      }
      document.getElementById("upload-heading").textContent = job ? "Upload more files" : "Media intake";
    }
    if (!job) {
      if (!uploadSection && previous && focus) {
        location.replace(document.body.dataset.homeUrl);
        return;
      }
      if (uploadSection && location.pathname !== document.body.dataset.homeUrl) {
        history.replaceState(null, "", document.body.dataset.homeUrl);
      }
      if (focus) {
        focusElement(document.querySelector("#job-panel [role='alert']"));
      }
      return;
    }
    enableBatchDownloads(job);
    if (previous && previous.id === job.dataset.jobId) {
      previous.details.forEach(function (id) {
        var details = document.getElementById(id);
        if (details) {
          details.open = true;
        }
      });
      Object.keys(previous.selections).forEach(function (id) {
        var select = document.getElementById(id);
        var saved = previous.selections[id];
        if (select && Array.from(select.options).some(function (option) { return option.value === saved.value; })) {
          select.value = saved.value;
          select.dataset.overridden = saved.overridden;
          updateOperationHelp(select);
        }
      });
      var batch = document.getElementById("batch-operation");
      if (batch) {
        batch.value = previous.batchValue;
        document.getElementById("apply-preset").disabled = !batch.value;
        document.getElementById("keep-individual").checked = previous.keepIndividual;
      }
    }
    if (focus) {
      focusElement(document.getElementById("job-heading"));
    } else if (previous && previous.hadFocus) {
      var focused = document.getElementById(previous.focusedID);
      if (focused) {
        focused.focus({ preventScroll: true });
      } else if (previous.state !== job.dataset.jobState) {
        focusElement(document.getElementById("job-heading"));
      }
    }
  }

  document.body.addEventListener("htmx:beforeRequest", function (event) {
    requestSources.set(event.detail.xhr, event.detail.elt);
  });
  document.body.addEventListener("htmx:beforeSwap", function (event) {
    var config = event.detail.requestConfig || {};
    var source = requestSources.get(event.detail.xhr) || event.detail.elt;
    if (event.detail.xhr.status >= 400) {
      // Keep selections, downloads, and retry controls on a failed action.
      event.detail.shouldSwap = false;
      showRequestError(event.detail.xhr, source, config.verb !== "get");
    } else {
      swapState = rememberJob(source && source.hasAttribute("data-remove-file"));
      if (config.verb !== "get") {
        clearNotices();
      }
    }
  });
  document.body.addEventListener("htmx:afterSwap", function (event) {
    var config = event.detail.requestConfig || {};
    syncJob(config.verb !== "get", swapState);
    swapState = null;
  });
  document.body.addEventListener("htmx:sendError", function (event) {
    var config = event.detail.requestConfig || {};
    showRequestError(null, requestSources.get(event.detail.xhr) || event.detail.elt, config.verb !== "get");
  });
  document.body.addEventListener("htmx:timeout", function (event) {
    showRequestError(null, event.detail.elt, true);
  });

  document.body.addEventListener("change", function (event) {
    if (event.target.matches("[data-operation]")) {
      event.target.dataset.overridden = "true";
      updateOperationHelp(event.target);
    }
    if (event.target.id === "batch-operation") {
      document.getElementById("apply-preset").disabled = !event.target.value;
    }
  });

  document.body.addEventListener("click", function (event) {
    var downloadAll = event.target.closest("[data-download-all]");
    if (downloadAll) {
      startBatchDownloads(downloadAll);
      return;
    }
    if (!event.target.closest("#apply-preset")) {
      return;
    }
    var value = document.getElementById("batch-operation").value;
    var keepIndividual = document.getElementById("keep-individual").checked;
    var applied = 0;
    var kept = 0;
    var incompatible = 0;
    document.querySelectorAll("[data-operation]").forEach(function (select) {
      if (!Array.from(select.options).some(function (option) { return option.value === value; })) {
        incompatible++;
      } else if (keepIndividual && select.dataset.overridden === "true") {
        kept++;
      } else {
        select.value = value;
        select.dataset.overridden = "";
        updateOperationHelp(select);
        applied++;
      }
    });
    document.getElementById("batch-status").textContent =
      "Applied to " + applied + " file" + plural(applied) + ". " +
      "Kept " + kept + " individual choice" + plural(kept) + ". " +
      incompatible + " file" + plural(incompatible) + " did not support this preset.";
  });

  window.addEventListener("popstate", function () { location.reload(); });

  enableBatchDownloads(document.getElementById("job"));

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
  var cancelUpload = document.getElementById("upload-cancel");
  var selectedList = document.getElementById("selected-files");
  var uploading = false;
  var currentRequest = null;

  function plural(count) {
    return count === 1 ? "" : "s";
  }

  function describeFiles() {
    var files = input.files;
    selectedList.replaceChildren();
    if (!files || files.length === 0) {
      fileList.textContent = "No files selected.";
      submit.disabled = true;
      noticeFor(form).replaceChildren();
      return;
    }
    var total = 0;
    for (var index = 0; index < files.length; index++) {
      total += files[index].size;
      var item = document.createElement("li");
      var name = document.createElement("span");
      name.textContent = files[index].name;
      var remove = document.createElement("button");
      remove.type = "button";
      remove.className = "text-button";
      remove.dataset.removeSelected = String(index);
      remove.setAttribute("aria-label", "Remove " + files[index].name);
      remove.textContent = "Remove";
      remove.disabled = uploading;
      item.append(name, remove);
      selectedList.append(item);
    }
    fileList.textContent =
      files.length + (files.length === 1 ? " file" : " files") +
      " selected (" + formatBytes(total) + ").";
    var exceedsCount = files.length > Number(form.dataset.maxFiles);
    var exceedsBytes = total > Number(form.dataset.maxBytes);
    submit.disabled = uploading || exceedsCount || exceedsBytes;
    if (exceedsCount || exceedsBytes) {
      showNotice("Select at most " + form.dataset.maxFiles + " files totaling no more than " +
        formatBytes(Number(form.dataset.maxBytes)) + ". Remove files before uploading.", noticeFor(form), false);
    } else {
      noticeFor(form).replaceChildren();
    }
  }

  selectedList.addEventListener("click", function (event) {
    var button = event.target.closest("[data-remove-selected]");
    if (!button || uploading) {
      return;
    }
    var removedIndex = Number(button.dataset.removeSelected);
    var transfer = new DataTransfer();
    Array.from(input.files).forEach(function (file, index) {
      if (index !== removedIndex) {
        transfer.items.add(file);
      }
    });
    input.files = transfer.files;
    describeFiles();
    focusElement(selectedList.querySelector("[data-remove-selected]") || dropZone);
  });

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
    if (!uploading) {
      input.click();
    }
  });

  dropZone.addEventListener("keydown", function (event) {
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      if (!uploading) {
        input.click();
      }
    }
  });

  cancelUpload.addEventListener("click", function () {
    if (currentRequest && !cancelUpload.hidden) {
      currentRequest.abort();
    }
  });

  form.addEventListener("submit", function (event) {
    event.preventDefault();
    if (uploading || submit.disabled || !input.files || input.files.length === 0) {
      return;
    }

    var data = new FormData();
    for (var index = 0; index < input.files.length; index++) {
      data.append("files", input.files[index]);
    }

    var request = new XMLHttpRequest();
    currentRequest = request;
    request.open("POST", form.getAttribute("action"), true);
    request.setRequestHeader("HX-Request", "true");
    uploading = true;
    input.disabled = true;
    clearNotices();
    submit.disabled = true;
    cancelUpload.hidden = false;
    selectedList.querySelectorAll("button").forEach(function (button) { button.disabled = true; });
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

    request.upload.addEventListener("load", function () {
      cancelUpload.hidden = true;
      progressText.textContent = "Upload sent. Checking file types...";
    });

    function finishUpload() {
      uploading = false;
      currentRequest = null;
      input.disabled = false;
      cancelUpload.hidden = true;
    }

    request.addEventListener("load", function () {
      finishUpload();
      progressText.textContent = request.status < 400 ? "Upload complete." : "Upload rejected.";
      progressWrap.hidden = request.status < 400;
      if (request.status < 400) {
        panel.innerHTML = request.responseText;
        if (window.htmx) {
          window.htmx.process(panel);
        }
        form.reset();
        describeFiles();
        var job = document.getElementById("job");
        if (job) {
          history.pushState(null, "", job.dataset.jobUrl);
        }
        syncJob(true, null);
      } else {
        describeFiles();
        showRequestError(request, form, true);
      }
    });

    request.addEventListener("error", function () {
      finishUpload();
      progressText.textContent = "Upload failed.";
      describeFiles();
      showRequestError(null, form, true);
    });

    request.addEventListener("abort", function () {
      finishUpload();
      progressText.textContent = "Upload canceled. Your selected files are ready to try again.";
      describeFiles();
      focusElement(submit);
    });

    request.send(data);
  });

  describeFiles();
})();
