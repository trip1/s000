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

  function animateLiveNumber(node) {
    if (!node) return
    var raw = (node.textContent || "").trim().replace(/,/g, "")
    if (!raw) return
    var target = Number(raw)
    if (!isFinite(target)) return

    var suffix = node.getAttribute("data-suffix") || ""
    var isDecimal = raw.indexOf(".") >= 0
    var decimals = isDecimal ? Math.min(2, (raw.split(".")[1] || "").length) : 0
    var from = target === 0 ? 0 : target * 0.86
    var duration = 340
    var start = performance.now()

    function format(value) {
      var output = decimals > 0 ? value.toFixed(decimals) : Math.round(value).toString()
      return output + suffix
    }

    function step(now) {
      var progress = Math.min(1, (now - start) / duration)
      var eased = 1 - Math.pow(1 - progress, 3)
      var value = from + (target - from) * eased
      node.textContent = format(value)
      if (progress < 1) {
        requestAnimationFrame(step)
      } else {
        node.textContent = format(target)
      }
    }

    requestAnimationFrame(step)
  }

  function bindDashboardMotion(root) {
    var scope = root || document
    var numbers = scope.querySelectorAll("[data-live-number]")
    if (!numbers.length) return
    for (var i = 0; i < numbers.length; i++) {
      animateLiveNumber(numbers[i])
    }
  }

  function bindDropUploadForm() {
    var form = document.querySelector("form[data-drop-upload-form]")
    if (!form) return
    var input = form.querySelector("[data-drop-input]")
    var folderInput = form.querySelector("[data-drop-folder-input]")
    var selectFilesButton = form.querySelector("[data-drop-select-files]")
    var selectFolderButton = form.querySelector("[data-drop-select-folder]")
    var zone = form.querySelector("[data-drop-zone]")
    var list = form.querySelector("[data-drop-list]")
    var status = form.querySelector("[data-drop-status]")
    var progress = form.querySelector("[data-drop-progress]")
    if (!input || !zone || !list) return
    var uploadItems = []

    function normalizePath(path) {
      return (path || "").replace(/^\/+/, "").replace(/\\/g, "/")
    }

    function renderFiles(items) {
      list.innerHTML = ""
      if (!items || items.length === 0) {
        var empty = document.createElement("li")
        empty.textContent = "No files selected."
        list.appendChild(empty)
        return
      }
      for (var i = 0; i < items.length; i++) {
        var item = document.createElement("li")
        item.textContent = items[i].path + " (" + items[i].file.size + " bytes)"
        list.appendChild(item)
      }
    }

    function setUploadItems(items, message) {
      uploadItems = items || []
      renderFiles(uploadItems)
      if (status) {
        status.textContent = message || ""
      }
      if (progress) {
        progress.hidden = true
        progress.value = 0
      }
    }

    function mergeUploadItems(existing, incoming) {
      var out = []
      var seen = {}
      var source = (existing || []).concat(incoming || [])
      for (var i = 0; i < source.length; i++) {
        var item = source[i]
        if (!item || !item.file) continue
        var key = (item.path || item.file.name) + "|" + item.file.size + "|" + item.file.lastModified
        if (seen[key]) continue
        seen[key] = true
        out.push(item)
      }
      return out
    }

    function collectFromFileList(files) {
      var out = []
      if (!files) return out
      for (var i = 0; i < files.length; i++) {
        var file = files[i]
        if (!file) continue
        var rel = normalizePath(file.webkitRelativePath || file.name)
        out.push({ file: file, path: rel || file.name })
      }
      return out
    }

    function readAllEntries(reader) {
      return new Promise(function (resolve) {
        var entries = []
        function pump() {
          reader.readEntries(
            function (batch) {
              if (!batch || batch.length === 0) {
                resolve(entries)
                return
              }
              for (var i = 0; i < batch.length; i++) {
                entries.push(batch[i])
              }
              pump()
            },
            function () {
              resolve(entries)
            }
          )
        }
        pump()
      })
    }

    function walkEntry(entry, parentPath) {
      if (!entry) return Promise.resolve([])
      var currentPath = normalizePath(parentPath ? parentPath + "/" + entry.name : entry.name)
      if (entry.isFile) {
        return new Promise(function (resolve) {
          entry.file(
            function (file) {
              var rel = currentPath || normalizePath(entry.fullPath || file.webkitRelativePath || file.name)
              resolve([{ file: file, path: rel || file.name }])
            },
            function () {
              resolve([])
            }
          )
        })
      }
      if (!entry.isDirectory) return Promise.resolve([])
      return readAllEntries(entry.createReader()).then(function (children) {
        if (!children.length) return []
        var tasks = []
        for (var i = 0; i < children.length; i++) {
          tasks.push(walkEntry(children[i], currentPath))
        }
        return Promise.all(tasks).then(function (groups) {
          var flattened = []
          for (var j = 0; j < groups.length; j++) {
            for (var k = 0; k < groups[j].length; k++) {
              flattened.push(groups[j][k])
            }
          }
          return flattened
        })
      })
    }

    function collectDroppedItems(dataTransfer) {
      if (!dataTransfer) return Promise.resolve([])
      if (dataTransfer.items && dataTransfer.items.length) {
        var tasks = []
        for (var i = 0; i < dataTransfer.items.length; i++) {
          var getEntry = dataTransfer.items[i].webkitGetAsEntry || dataTransfer.items[i].getAsEntry
          if (!getEntry) continue
          var entry = getEntry.call(dataTransfer.items[i])
          if (!entry) continue
          tasks.push(walkEntry(entry))
        }
        if (tasks.length > 0) {
          return Promise.all(tasks).then(function (groups) {
            var flattened = []
            for (var j = 0; j < groups.length; j++) {
              for (var k = 0; k < groups[j].length; k++) {
                flattened.push(groups[j][k])
              }
            }
            return flattened
          })
        }
      }
      return Promise.resolve(collectFromFileList(dataTransfer.files))
    }

    function setDragState(active) {
      if (active) {
        zone.classList.add("is-drag-over")
      } else {
        zone.classList.remove("is-drag-over")
      }
    }

    zone.addEventListener("click", function () {
      input.click()
    })

    if (selectFilesButton) {
      selectFilesButton.addEventListener("click", function () {
        input.click()
      })
    }

    if (selectFolderButton && folderInput) {
      selectFolderButton.addEventListener("click", function () {
        folderInput.click()
      })
    }

    zone.addEventListener("keydown", function (evt) {
      if (evt.key === "Enter" || evt.key === " ") {
        evt.preventDefault()
        input.click()
      }
    })

    input.addEventListener("change", function () {
      var items = collectFromFileList(input.files)
      uploadItems = mergeUploadItems(uploadItems, items)
      setUploadItems(uploadItems, uploadItems.length + " file(s) ready to upload.")
      input.value = ""
    })

    if (folderInput) {
      folderInput.addEventListener("change", function () {
        var items = collectFromFileList(folderInput.files)
        uploadItems = mergeUploadItems(uploadItems, items)
        setUploadItems(uploadItems, uploadItems.length + " file(s) selected with folder paths preserved.")
        folderInput.value = ""
      })
    }

    zone.addEventListener("dragenter", function (evt) {
      evt.preventDefault()
      setDragState(true)
    })

    zone.addEventListener("dragover", function (evt) {
      evt.preventDefault()
      setDragState(true)
    })

    zone.addEventListener("dragleave", function () {
      setDragState(false)
    })

    zone.addEventListener("drop", function (evt) {
      evt.preventDefault()
      setDragState(false)
      collectDroppedItems(evt.dataTransfer).then(function (items) {
        if (!items.length) {
          setUploadItems([], "No files were detected in drop payload.")
          return
        }
        uploadItems = mergeUploadItems(uploadItems, items)
        setUploadItems(uploadItems, uploadItems.length + " file(s) staged from drag and drop.")
      })
    })

    form.addEventListener("submit", function (evt) {
      if (!window.XMLHttpRequest || !window.FormData) return
      if (!uploadItems.length) {
        if (status) status.textContent = "Choose files or a folder before uploading."
        return
      }
      evt.preventDefault()

      var data = new FormData()
      var base = new FormData(form)
      base.forEach(function (value, key) {
        if (key === "files" || key === "file" || key === "file_key") return
        data.append(key, value)
      })
      for (var i = 0; i < uploadItems.length; i++) {
        var item = uploadItems[i]
        data.append("files", item.file, item.file.name)
        data.append("file_key", item.path || item.file.name)
      }

      var xhr = new XMLHttpRequest()
      xhr.open("POST", form.action, true)
      var token = csrfToken()
      if (token) xhr.setRequestHeader("X-CSRF-Token", token)
      if (progress) {
        progress.hidden = false
        progress.value = 0
      }
      if (status) status.textContent = "Uploading " + uploadItems.length + " file(s)..."

      xhr.upload.addEventListener("progress", function (e) {
        if (!e.lengthComputable || !progress) return
        var pct = Math.floor((e.loaded / e.total) * 100)
        progress.value = pct
        if (status) status.textContent = "Upload " + pct + "%"
      })

      xhr.onreadystatechange = function () {
        if (xhr.readyState !== 4) return
        if (xhr.status >= 200 && xhr.status < 400) {
          if (xhr.responseURL) {
            window.location.href = xhr.responseURL
          } else {
            window.location.reload()
          }
          return
        }
        if (status) {
          status.textContent = "Upload failed. " + (xhr.responseText || "Please retry.")
        }
      }

      xhr.onerror = function () {
        if (status) status.textContent = "Upload failed due to network error."
      }

      xhr.send(data)
    })

    setUploadItems(collectFromFileList(input.files), "")
  }

  document.addEventListener("DOMContentLoaded", function () {
    bindUploadForm()
    bindDropUploadForm()
    bindDashboardMotion(document)
  })

  document.addEventListener("htmx:afterSwap", function (evt) {
    bindDashboardMotion(evt && evt.target ? evt.target : document)
  })
})()
