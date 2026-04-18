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
