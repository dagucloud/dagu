// Opens a window that covers the main screen above the applications'
// windows and lets every click through, as Notification Center's does while
// a banner shows; then runs until the test ends the process.
ObjC.import('Cocoa');

const app = $.NSApplication.sharedApplication;
app.setActivationPolicy($.NSApplicationActivationPolicyAccessory);

const window = $.NSWindow.alloc.initWithContentRectStyleMaskBackingDefer(
  $.NSScreen.mainScreen.frame, $.NSWindowStyleMaskBorderless, $.NSBackingStoreBuffered, false);
window.title = 'dagu-overlay-e2e';
window.level = 21;
window.opaque = false;
window.backgroundColor = $.NSColor.clearColor;
window.ignoresMouseEvents = true;
window.releasedWhenClosed = false;
window.orderFrontRegardless;
app.run;
