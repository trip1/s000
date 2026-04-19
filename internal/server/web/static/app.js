;(function () {
  function csrfToken() {
    var meta = document.querySelector("meta[name='csrf-token']")
    return meta ? meta.getAttribute("content") : ""
  }

  document.addEventListener("htmx:configRequest", function (evt) {
    var token = csrfToken()
    if (token) {
      evt.detail.headers["X-CSRF-Token"] = token
    }
  })

  function bindUploadForm() {
    var form = document.querySelector("form[data-upload-form]")
    if (!form) return

    var progress = document.getElementById("upload-progress")
    var status = document.getElementById("upload-status")
    var keyInput = form.querySelector("[data-upload-key]")
    var fileInput = form.querySelector("[data-upload-file]")
    var prefixInput = form.querySelector("input[name='prefix']")
    var prefillButton = form.querySelector("[data-upload-prefill]")

    function normalizePrefix(prefix) {
      var value = (prefix || "").trim()
      if (!value) return ""
      if (value.charAt(value.length - 1) !== "/") value += "/"
      return value
    }

    if (prefillButton) {
      prefillButton.addEventListener("click", function () {
        if (!fileInput || !fileInput.files || fileInput.files.length === 0) {
          if (status) status.textContent = "Choose a file first to prefill object key."
          return
        }
        if (!keyInput) return
        var filename = fileInput.files[0].name || ""
        if (!filename) {
          if (status) status.textContent = "Selected file has no name."
          return
        }
        var prefix = normalizePrefix(prefixInput ? prefixInput.value : "")
        keyInput.value = prefix + filename
        if (status) status.textContent = "Object key prefilled from current folder."
      })
    }

    form.addEventListener("submit", function (evt) {
      if (!window.XMLHttpRequest || !window.FormData) return
      evt.preventDefault()

      var xhr = new XMLHttpRequest()
      xhr.open("POST", form.action, true)
      var token = csrfToken()
      if (token) xhr.setRequestHeader("X-CSRF-Token", token)

      if (progress) {
        progress.hidden = false
        progress.value = 0
      }
      if (status) status.textContent = "Uploading..."

      xhr.upload.addEventListener("progress", function (e) {
        if (!e.lengthComputable || !progress) return
        var pct = Math.floor((e.loaded / e.total) * 100)
        progress.value = pct
        if (status) status.textContent = "Upload " + pct + "%"
      })

      xhr.onreadystatechange = function () {
        if (xhr.readyState !== 4) return
        if (xhr.status >= 200 && xhr.status < 400) {
          if (status) status.textContent = "Upload complete"
          if (xhr.responseURL) {
            window.location.href = xhr.responseURL
          }
        } else {
          if (status) status.textContent = "Upload failed. " + (xhr.responseText || "Please retry.")
        }
      }

      xhr.onerror = function () {
        if (status) status.textContent = "Upload failed due to network error."
      }

      var data = new FormData(form)
      xhr.send(data)
    })
  }

  document.addEventListener("DOMContentLoaded", bindUploadForm)
})()
