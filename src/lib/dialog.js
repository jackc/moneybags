/** Native dialogs provide focus trapping, Escape handling, and focus restore. */
export function openDialog(node, onclose) {
  const previousOverflow = document.body.style.overflow;
  document.body.style.overflow = 'hidden';
  const cancel = (event) => {
    event.preventDefault();
    onclose?.();
  };
  node.addEventListener('cancel', cancel);
  node.showModal();
  return {
    update(callback) {
      onclose = callback;
    },
    destroy() {
      node.removeEventListener('cancel', cancel);
      node.close();
      document.body.style.overflow = previousOverflow;
    }
  };
}
