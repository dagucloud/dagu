// Opens a window and then, as the argument asks, a sheet on it, a form of
// the application's own as a sheet, a modal alert, or a panel that is not
// modal; then runs until the test ends the process.
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
    case 'form': {
      const form = $.NSWindow.alloc.initWithContentRectStyleMaskBackingDefer(
        $.NSMakeRect(0, 0, 300, 140), $.NSWindowStyleMaskTitled, $.NSBackingStoreBuffered, false);
      form.title = 'Dagu form sheet';
      const field = $.NSTextField.alloc.initWithFrame($.NSMakeRect(20, 80, 200, 24));
      field.setAccessibilityLabel('Dagu form field');
      form.contentView.addSubview(field);
      const save = $.NSButton.buttonWithTitleTargetAction('Dagu form save', null, null);
      save.frame = $.NSMakeRect(20, 20, 160, 32);
      form.contentView.addSubview(save);
      host.beginSheetCompletionHandler(form, null);
      form.makeFirstResponder(field);
      break;
    }
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
