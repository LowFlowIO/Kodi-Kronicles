(function () {
  var script = document.currentScript;
  if (!script) return;
  var src = new URL(script.src);
  var dir = src.pathname.replace(/\/[^/]*$/, "/");
  var theme = script.getAttribute("data-theme") || "garden";
  var targetSel = script.getAttribute("data-target");
  var mount = targetSel ? document.querySelector(targetSel) : script.previousElementSibling;
  if (!mount) {
    mount = document.createElement("div");
    script.parentNode.insertBefore(mount, script);
  }
  var frame = document.createElement("iframe");
  frame.title = "Now playing";
  frame.loading = "lazy";
  frame.src = src.origin + dir + "embed.html?theme=" + encodeURIComponent(theme);
  frame.style.cssText = "border:0;width:100%;height:210px;display:block;background:transparent";
  mount.innerHTML = "";
  mount.appendChild(frame);
})();
