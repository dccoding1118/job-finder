// Routes one Cake tab to the mode its current screen calls for, and owns the
// page context the Side Panel reads. Cake is a single-page application: the
// user reaches a JD by clicking a list entry, which changes the URL without
// loading a document, so a mode is chosen from the URL on every navigation
// instead of from which script Chrome injected.
(() => {
  const modes = globalThis.jobfinder.cake;
  const JOB_PATH = /^\/companies\/[^/]+\/jobs\/[^/]+/;
  const LIST_PATH = /^\/jobs(\/|$)/;

  let context = { kind: "unsupported", status: "unsupported" };
  let active = null;

  chrome.runtime.onMessage.addListener((message, _sender, respond) => {
    if (message?.type !== "get-page-context") return;
    respond(context);
  });

  function publish(next) {
    context = next;
    chrome.runtime.sendMessage({ type: "page-context-updated", context: next }).catch(() => {});
  }

  function modeName() {
    if (JOB_PATH.test(location.pathname)) return "job";
    if (LIST_PATH.test(location.pathname)) return "list";
    return "";
  }

  function route() {
    const name = modeName();
    // A mode is left running across a navigation inside its own kind of screen:
    // the list mode already follows the entries Cake injects when the user
    // changes a filter or turns a page.
    if (name === active?.name) return;
    active?.handle?.stop?.();
    active = null;
    if (!name) {
      publish({ kind: "unsupported", status: "unsupported" });
      return;
    }
    active = { name, handle: modes[name].start(publish) };
  }

  globalThis.jobfinder.onNavigate(route);
  route();
})();
