// Runs synchronously before first paint (loaded from index.html, CSP 'self').
// Keep in sync with src/lib/theme.tsx (THEME_STORAGE_KEY, resolution rules).
(function () {
  var pref = "system";
  try {
    var stored = window.localStorage.getItem("bekup.theme");
    if (stored === "light" || stored === "dark" || stored === "system") pref = stored;
  } catch (_) {
    /* storage unavailable: fall back to the system preference */
  }
  var dark =
    pref === "dark" ||
    (pref === "system" && window.matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.classList.toggle("dark", dark);
})();
