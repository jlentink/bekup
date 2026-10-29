/**
 * Saves text held in memory as a file without showing it (D-179 item 8):
 * a local blob: link, never a data: URL (the CSP allows neither data: nor
 * navigation to it), clicked once and revoked right after.
 */
export function downloadText(name: string, text: string): void {
  const url = URL.createObjectURL(new Blob([text], { type: "text/plain;charset=utf-8" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.rel = "noopener";
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => {
    URL.revokeObjectURL(url);
  }, 0);
}
