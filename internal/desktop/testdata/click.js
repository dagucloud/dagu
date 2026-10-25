// Opens a window whose view prints "clicked" for every mouse press it
// receives, then runs until the test ends the process. Like most views, and
// unlike a standard button, it takes no first click: a press on the window
// in the background only brings it forward.
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

const app = $.NSApplication.sharedApplication;
app.setActivationPolicy($.NSApplicationActivationPolicyRegular);

const window = $.NSWindow.alloc.initWithContentRectStyleMaskBackingDefer(
  $.NSMakeRect(1100, 200, 300, 200), $.NSWindowStyleMaskTitled, $.NSBackingStoreBuffered, false);
window.title = 'dagu-click-e2e';
window.releasedWhenClosed = false;
window.contentView = $.ClickProbe.alloc.initWithFrame($.NSMakeRect(0, 0, 300, 200));
window.makeKeyAndOrderFront(null);
app.activateIgnoringOtherApps(true);
app.run;
