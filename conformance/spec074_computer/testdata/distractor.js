// A window of another app beside the elements window, for the desktop bench:
// it holds a 保存 button like the elements window's, and writes the file
// named on the command line when that button is pressed, so a run that
// strays out of the app it works in is caught. Run with:
// osascript -l JavaScript distractor.js <marker file>
ObjC.import('Cocoa');

const args = $.NSProcessInfo.processInfo.arguments.js.map((a) => a.js);
const marker = args[args.length - 1];
const app = $.NSApplication.sharedApplication;
app.setActivationPolicy($.NSApplicationActivationPolicyRegular);

const window = $.NSWindow.alloc.initWithContentRectStyleMaskBackingDefer(
  $.NSMakeRect(720, 400, 300, 160),
  $.NSWindowStyleMaskTitled,
  $.NSBackingStoreBuffered, false);
window.title = 'Dagu distractor';
window.releasedWhenClosed = false;

ObjC.registerSubclass({
  name: 'DaguDistractorTarget',
  methods: {
    'save:': {
      types: ['void', ['id']],
      implementation: function () {
        $(marker).writeToFileAtomicallyEncodingError($(marker), true, $.NSUTF8StringEncoding, null);
      },
    },
  },
});
const target = $.DaguDistractorTarget.alloc.init;
const save = $.NSButton.buttonWithTitleTargetAction('保存', target, 'save:');
save.frame = $.NSMakeRect(100, 60, 100, 32);
save.identifier = 'distractorSave';
window.contentView.addSubview(save);
window.orderFront(null);
app.run;
