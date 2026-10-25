// Opens a window whose view prints "clicked" for every mouse press it
// receives, then runs until the test ends the process. Like most views, and
// unlike a standard button, it takes no first click: a press on the window
// in the background only brings it forward. Given "overlay", it also covers
// the screen with a window of its own that lets every click through.
ObjC.import('Cocoa');

ObjC.registerSubclass({
  name: 'ClickProbe',
  superclass: 'NSView',
  methods: {
    'mouseDown:': {
      types: ['void', ['id']],
      implementation: function () {
        $.NSFileHandle.fileHandleWithStandardOutput.writeData($('clicked\n').dataUsingEncoding($.NSUTF8StringEncoding));
      },
    },
  },
});

function run(argv) {
  const app = $.NSApplication.sharedApplication;
  app.setActivationPolicy($.NSApplicationActivationPolicyRegular);

  const window = $.NSWindow.alloc.initWithContentRectStyleMaskBackingDefer(
    $.NSMakeRect(1100, 200, 300, 200), $.NSWindowStyleMaskTitled, $.NSBackingStoreBuffered, false);
  window.title = 'dagu-click-e2e';
  window.releasedWhenClosed = false;
  window.contentView = $.ClickProbe.alloc.initWithFrame($.NSMakeRect(0, 0, 300, 200));
  window.makeKeyAndOrderFront(null);

  if (argv.includes('overlay')) {
    const overlay = $.NSWindow.alloc.initWithContentRectStyleMaskBackingDefer(
      $.NSScreen.mainScreen.frame, $.NSWindowStyleMaskBorderless, $.NSBackingStoreBuffered, false);
    overlay.title = 'dagu-click-overlay-e2e';
    overlay.level = 21;
    overlay.opaque = false;
    overlay.backgroundColor = $.NSColor.clearColor;
    overlay.ignoresMouseEvents = true;
    overlay.releasedWhenClosed = false;
    overlay.orderFrontRegardless;
  }
  app.activateIgnoringOtherApps(true);
  app.run;
}
