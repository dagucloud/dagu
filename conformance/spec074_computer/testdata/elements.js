// A window with named controls, for tests that read elements on macOS: the
// twin of elements.ps1. Each control's identifier is what Accessibility
// reports as its AXIdentifier. Run with: osascript -l JavaScript elements.js
ObjC.import('Cocoa');

const app = $.NSApplication.sharedApplication;
app.setActivationPolicy($.NSApplicationActivationPolicyRegular);

const width = 600;
const height = 540;
const window = $.NSWindow.alloc.initWithContentRectStyleMaskBackingDefer(
  $.NSMakeRect(100, 200, width, height),
  $.NSWindowStyleMaskTitled | $.NSWindowStyleMaskClosable | $.NSWindowStyleMaskResizable,
  $.NSBackingStoreBuffered, false);
window.title = 'Dagu elements test';
window.releasedWhenClosed = false;

// place puts a control at a position measured from the top left of its
// parent, as the WinForms twin does, and names it.
function place(parent, control, id, x, y, w, h) {
  const top = parent.isFlipped ? y : parent.frame.size.height - y - h;
  control.frame = $.NSMakeRect(x, top, w, h);
  control.identifier = id;
  parent.addSubview(control);
  return control;
}

function label(text) {
  return $.NSTextField.labelWithString(text);
}

function field() {
  return $.NSTextField.alloc.initWithFrame($.NSMakeRect(0, 0, 10, 10));
}

const content = window.contentView;
let status;

// The target of the controls' actions says what they did in the status
// label, for tests that act on them.
ObjC.registerSubclass({
  name: 'DaguElementsTarget',
  methods: {
    'save:': { types: ['void', ['id']], implementation: function () { status.stringValue = '保存しました'; } },
    'agree:': { types: ['void', ['id']], implementation: function (sender) { status.stringValue = '同意: ' + (Number(sender.state) === 1 ? 'True' : 'False'); } },
    'method:': { types: ['void', ['id']], implementation: function (sender) { status.stringValue = '支払方法: ' + sender.titleOfSelectedItem.js; } },
    'tier:': { types: ['void', ['id']], implementation: function (sender) { status.stringValue = '区分: ' + sender.title.js; } },
    'confirm:': {
      types: ['void', ['id']],
      implementation: function () {
        const alert = $.NSAlert.alloc.init;
        alert.messageText = '確認しました';
        alert.runModal;
      },
    },
  },
});
const target = $.DaguElementsTarget.alloc.init;

const lines = [['りんご', '1,200'], ['みかん', '3,400']];
ObjC.registerSubclass({
  name: 'DaguElementsLines',
  methods: {
    'numberOfRowsInTableView:': { types: ['long', ['id']], implementation: function () { return lines.length; } },
    'tableView:objectValueForTableColumn:row:': {
      types: ['id', ['id', 'id', 'long']],
      implementation: function (table, column, row) {
        return $(lines[row][column.identifier.js === 'item' ? 0 : 1]);
      },
    },
  },
});
const linesSource = $.DaguElementsLines.alloc.init;

place(content, label('金額'), 'lblAmount', 20, 20, 60, 24);
place(content, field(), 'amountBox', 90, 20, 200, 24);
place(content, $.NSButton.buttonWithTitleTargetAction('保存', target, 'save:'), 'saveButton', 90, 60, 90, 28);
place(content, $.NSButton.checkboxWithTitleTargetAction('同意する', target, 'agree:'), 'agreeBox', 90, 100, 120, 24);
const method = place(content, $.NSPopUpButton.alloc.initWithFramePullsDown($.NSMakeRect(0, 0, 10, 10), false), 'methodBox', 90, 140, 200, 26);
['銀行振込', 'クレジット', '現金'].forEach((item) => method.addItemWithTitle(item));
method.target = target;
method.action = 'method:';
method.accessibilityLabel = '支払方法';

status = place(content, label('待機中'), 'statusLabel', 320, 20, 240, 24);
['通常', '優先', '至急'].forEach((name, i) => {
  place(content, $.NSButton.radioButtonWithTitleTargetAction(name, target, 'tier:'), 'tier' + i, 320, 60 + i * 22, 120, 20);
});
const submit = place(content, $.NSButton.buttonWithTitleTargetAction('送信', target, 'save:'), 'submitButton', 450, 60, 90, 28);
submit.enabled = false;
place(content, $.NSButton.buttonWithTitleTargetAction('確認', target, 'confirm:'), 'confirmButton', 450, 140, 90, 28);

const group = place(content, $.NSBox.alloc.initWithFrame($.NSMakeRect(0, 0, 10, 10)), 'paymentGroup', 20, 190, 540, 150);
group.title = '支払情報';
const inner = group.contentView;
place(inner, label('金額'), 'lblPaymentAmount', 20, 20, 60, 24);
place(inner, field(), 'paymentAmountBox', 90, 20, 200, 24);
place(inner, $.NSButton.buttonWithTitleTargetAction('保存', target, 'save:'), 'paymentSaveButton', 90, 60, 90, 28);

// A table with column headers, for tests that read rows by header.
const scroll = place(content, $.NSScrollView.alloc.initWithFrame($.NSMakeRect(0, 0, 10, 10)), 'linesScroll', 20, 350, 540, 120);
const table = $.NSTableView.alloc.initWithFrame($.NSMakeRect(0, 0, 540, 120));
table.identifier = 'linesList';
[['item', '品目'], ['amount', '金額']].forEach(([id, title]) => {
  const column = $.NSTableColumn.alloc.initWithIdentifier(id);
  column.title = title;
  column.width = 260;
  table.addTableColumn(column);
});
table.dataSource = linesSource;
scroll.documentView = table;
scroll.hasVerticalScroller = true;
table.reloadData;

window.makeKeyAndOrderFront(null);
app.activateIgnoringOtherApps(true);
app.run;
