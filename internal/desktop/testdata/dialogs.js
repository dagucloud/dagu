// Opens a window and then, as the argument asks, a sheet on it, a modal
// alert, or a panel that is not modal; then runs until the test ends the
// process.
ObjC.import('Cocoa');

function run(argv) {
  const app = $.NSApplication.sharedApplication;
  app.setActivationPolicy($.NSApplicationActivationPolicyRegular);

  const host = $.NSWindow.alloc.initWithContentRectStyleMaskBackingDefer(
    $.NSMakeRect(200, 200, 400, 300), $.NSWindowStyleMaskTitled | $.NSWindowStyleMaskClosable,
    $.NSBackingStoreBuffered, false);
  host.title = 'Dagu dialogs test';
  host.releasedWhenClosed = false;
  host.makeKeyAndOrderFront(null);
  app.activateIgnoringOtherApps(true);

  const alert = $.NSAlert.alloc.init;
  switch (argv[0]) {
    case 'sheet':
      alert.messageText = 'Dagu sheet question';
      alert.beginSheetModalForWindowCompletionHandler(host, null);
      break;
    case 'alert':
      // A modal alert runs once the application has finished launching,
      // so it comes to the front as the active application's.
      alert.messageText = 'Dagu alert question';
      $.NSTimer.scheduledTimerWithTimeIntervalRepeatsBlock(0.2, false, () => alert.runModal);
      break;
    default: {
      const panel = $.NSPanel.alloc.initWithContentRectStyleMaskBackingDefer(
        $.NSMakeRect(300, 300, 240, 120), $.NSWindowStyleMaskTitled, $.NSBackingStoreBuffered, false);
      panel.title = 'Dagu panel test';
      panel.releasedWhenClosed = false;
      const label = $.NSTextField.labelWithString('Dagu panel note');
      label.frame = $.NSMakeRect(20, 40, 200, 24);
      panel.contentView.addSubview(label);
      panel.makeKeyAndOrderFront(null);
    }
  }
  app.run;
}
