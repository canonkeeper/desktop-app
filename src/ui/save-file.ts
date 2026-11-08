/** Hands a downloaded file to the browser, which saves it under its name: a book, a chapter with revisions. */
export function saveFile({ blob, fileName }: { blob: Blob; fileName: string }) {
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = fileName;
  document.body.append(link);
  link.click();
  link.remove();
  // Some browsers start the download after the click returns: the address has to outlive it.
  window.setTimeout(() => URL.revokeObjectURL(url), 60_000);
}
