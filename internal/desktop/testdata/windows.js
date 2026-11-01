// Opens two windows for the macOS window tests: one a person can resize,
// and one of fixed size, then runs until the test ends the process.
ObjC.import('Cocoa');

const app = $.NSApplication.sharedApplication;
app.setActivationPolicy($.NSApplicationActivationPolicyRegular);

function open(title, x, y, resizable) {
  let style = $.NSWindowStyleMaskTitled | $.NSWindowStyleMaskClosable | $.NSWindowStyleMaskMiniaturizable;
  if (resizable) {
    style |= $.NSWindowStyleMaskResizable;
  }
  const window = $.NSWindow.alloc.initWithContentRectStyleMaskBackingDefer(
    $.NSMakeRect(x, y, 400, 300), style, $.NSBackingStoreBuffered, false);
  window.title = title;
  window.releasedWhenClosed = false;
  window.makeKeyAndOrderFront(null);
  return window;
}

open('dagu-fixed-e2e', 600, 200, false);
open('dagu-window-e2e', 100, 200, true);
app.activateIgnoringOtherApps(true);
app.run;
