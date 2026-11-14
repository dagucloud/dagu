// A window of one color for the capture tests, which opens without taking
// the focus and stays above the windows opened before it; then runs until
// the test ends the process. Run with:
// osascript -l JavaScript color.js <title> <magenta|green> <x> <y> <width> <height>
ObjC.import('Cocoa');

function run(argv) {
  const [title, color, x, y, width, height] = argv;
  const app = $.NSApplication.sharedApplication;
  app.setActivationPolicy($.NSApplicationActivationPolicyAccessory);
  const window = $.NSWindow.alloc.initWithContentRectStyleMaskBackingDefer(
    $.NSMakeRect(Number(x), Number(y), Number(width), Number(height)),
    $.NSWindowStyleMaskTitled, $.NSBackingStoreBuffered, false);
  window.title = title;
  window.releasedWhenClosed = false;
  window.backgroundColor = color === 'magenta' ? $.NSColor.magentaColor : $.NSColor.greenColor;
  window.orderFrontRegardless;
  app.run;
}
