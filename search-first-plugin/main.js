/**
 * Search First — PI-Desktop plugin entry.
 *
 * The host injects the global `pi` object. Every call is gated by the
 * permissions declared in manifest.json, so widening what this file does
 * usually means widening `permissions` too.
 */

async function onLoad() {
  await pi.ui.showToast("Search First skills are active.");
}

async function onUnload() {
  // nothing to tear down
}

module.exports = { onLoad, onUnload };
