# A window with named controls, for tests that read elements. Each
# control's Name is what UI Automation reports as its AutomationId.
param()

Add-Type -AssemblyName System.Windows.Forms, System.Drawing

# Keep the project directory free, so the test can remove it.
Set-Location $env:TEMP

$form = New-Object System.Windows.Forms.Form
$form.Text = 'Dagu elements test'
$form.StartPosition = 'Manual'
$form.Location = New-Object System.Drawing.Point(100, 100)
$form.Size = New-Object System.Drawing.Size(600, 540)
$form.TopMost = $true
$form.Add_Shown({ $form.Activate() })

function Add-Control($parent, $type, $name, $text, $x, $y, $w, $h) {
  $c = New-Object "System.Windows.Forms.$type"
  $c.Name = $name
  $c.Text = $text
  $c.Location = New-Object System.Drawing.Point($x, $y)
  $c.Size = New-Object System.Drawing.Size($w, $h)
  $parent.Controls.Add($c)
  return $c
}

[void](Add-Control $form 'Label' 'lblAmount' '金額' 20 20 60 24)
[void](Add-Control $form 'TextBox' 'amountBox' '' 90 20 200 24)
$save = Add-Control $form 'Button' 'saveButton' '保存' 90 60 90 28
$agree = Add-Control $form 'CheckBox' 'agreeBox' '同意する' 90 100 120 24
$method = Add-Control $form 'ComboBox' 'methodBox' '' 90 140 200 24
$method.DropDownStyle = 'DropDownList'
[void]$method.Items.AddRange(@('銀行振込', 'クレジット', '現金'))
$method.AccessibleName = '支払方法'

# The status label says what the controls did, for tests that act on them.
$status = Add-Control $form 'Label' 'statusLabel' '待機中' 320 20 240 24
$save.Add_Click({ $status.Text = '保存しました' })
$agree.Add_CheckedChanged({ $status.Text = '同意: ' + $agree.Checked })
$method.Add_SelectedIndexChanged({ $status.Text = '支払方法: ' + $method.Text })
$tier = Add-Control $form 'ListView' 'tierList' '' 320 60 120 60
$tier.View = 'List'
$tier.MultiSelect = $false
foreach ($name in @('通常', '優先', '至急')) { [void]$tier.Items.Add($name) }
$tier.Add_SelectedIndexChanged({ if ($tier.SelectedItems.Count -gt 0) { $status.Text = '区分: ' + $tier.SelectedItems[0].Text } })
$submit = Add-Control $form 'Button' 'submitButton' '送信' 320 140 90 28
$submit.Enabled = $false
$confirm = Add-Control $form 'Button' 'confirmButton' '確認' 450 140 90 28
$confirm.Add_Click({ [void][System.Windows.Forms.MessageBox]::Show($form, '確認しました', '確認') })
$group = Add-Control $form 'GroupBox' 'paymentGroup' '支払情報' 20 190 540 150
[void](Add-Control $group 'Label' 'lblPaymentAmount' '金額' 20 30 60 24)
[void](Add-Control $group 'TextBox' 'paymentAmountBox' '' 90 30 200 24)
$paymentSave = Add-Control $group 'Button' 'paymentSaveButton' '保存' 90 70 90 28
$paymentSave.Add_Click({ $status.Text = '保存しました' })

# A table in Details view, for tests that read rows by column header.
$lines = Add-Control $form 'ListView' 'linesList' '' 20 350 540 100
$lines.View = 'Details'
$lines.FullRowSelect = $true
[void]$lines.Columns.Add('品目', 260)
[void]$lines.Columns.Add('金額', 260)
[void]$lines.Items.Add('りんご').SubItems.Add('1,200')
[void]$lines.Items.Add('みかん').SubItems.Add('3,400')

[void]$form.ShowDialog()
