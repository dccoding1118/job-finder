// Client-side navigation notice, shared by every platform script. Chrome injects
// a content script once per document load, so on a single-page platform the
// script that happens to be alive is the one that matched the first URL the user
// opened. Every later screen is reached without a document load, which is why a
// platform script routes on this signal rather than on its own injection.
globalThis.jobfinder = globalThis.jobfinder || {};

globalThis.jobfinder.onNavigate = (handler) => {
  let current = location.href;
  const notify = () => {
    if (location.href === current) return;
    current = location.href;
    handler();
  };
  // The History API changes the URL without an event of its own, so the two
  // methods that do it are wrapped. `popstate` covers the back and forward
  // buttons, which the History API does not report.
  for (const name of ["pushState", "replaceState"]) {
    const original = history[name].bind(history);
    history[name] = (...args) => {
      const result = original(...args);
      // The URL is only current once the call returned, so the check waits for
      // the frame to settle rather than reading the old one.
      setTimeout(notify, 0);
      return result;
    };
  }
  addEventListener("popstate", notify);
  addEventListener("hashchange", notify);
};
