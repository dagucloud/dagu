// Opens a window whose content sits in nested groups, the innermost naming
// the window as its parent though the window reaches it through the groups,
// as Electron's web area does; then runs until the test ends the process.
ObjC.import('Cocoa');

const app = $.NSApplication.sharedApplication;
app.setActivationPolicy($.NSApplicationActivationPolicyRegular);

const host = $.NSWindow.alloc.initWithContentRectStyleMaskBackingDefer(
  $.NSMakeRect(200, 200, 400, 300), $.NSWindowStyleMaskTitled | $.NSWindowStyleMaskClosable,
  $.NSBackingStoreBuffered, false);
host.title = 'Dagu skipped parent test';
host.releasedWhenClosed = false;

ObjC.registerSubclass({
  name: 'SkippedParentView',
  superclass: 'NSView',
  methods: {
    accessibilityParent: {
      types: ['id', []],
      implementation: function () { return host; },
    },
  },
});

function group(kind, frame) {
  const view = kind.alloc.initWithFrame(frame);
  view.accessibilityElement = true;
  view.accessibilityRole = $.NSAccessibilityGroupRole;
  return view;
}

const outer = group($.NSView, $.NSMakeRect(0, 0, 400, 300));
const inner = group($.NSView, $.NSMakeRect(0, 0, 400, 300));
const content = group($.SkippedParentView, $.NSMakeRect(0, 0, 400, 300));
content.accessibilityIdentifier = 'content';
const button = $.NSButton.buttonWithTitleTargetAction('Go', null, null);
button.frame = $.NSMakeRect(150, 130, 100, 32);
button.identifier = 'goButton';
content.addSubview(button);
inner.addSubview(content);
outer.addSubview(inner);
host.contentView.addSubview(outer);

host.makeKeyAndOrderFront(null);
app.activateIgnoringOtherApps(true);
app.run;
