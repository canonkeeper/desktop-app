export function captureMessageText(content: HTMLElement): { body: string; quote: string } {
  const selection = window.getSelection();
  if (selection && !selection.isCollapsed && selection.rangeCount) {
    const range = selection.getRangeAt(0);
    if (content.contains(range.startContainer) && content.contains(range.endContainer)) {
      const selected = selection.toString().trim();
      if (selected) return { body: selected, quote: range.toString().trim() };
    }
  }
  return { body: (content.innerText ?? content.textContent ?? '').trim(), quote: (content.textContent ?? '').trim() };
}
