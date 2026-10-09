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
$form.Size = New-Object System.Drawing.Size(600, 400)
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
[void](Add-Control $form 'Button' 'saveButton' '保存' 90 60 90 28)
[void](Add-Control $form 'CheckBox' 'agreeBox' '同意する' 90 100 120 24)
$method = Add-Control $form 'ComboBox' 'methodBox' '' 90 140 200 24
$method.DropDownStyle = 'DropDownList'
[void]$method.Items.AddRange(@('銀行振込', 'クレジット', '現金'))
$method.AccessibleName = '支払方法'
$group = Add-Control $form 'GroupBox' 'paymentGroup' '支払情報' 20 190 540 150
[void](Add-Control $group 'Label' 'lblPaymentAmount' '金額' 20 30 60 24)
[void](Add-Control $group 'TextBox' 'paymentAmountBox' '' 90 30 200 24)
[void](Add-Control $group 'Button' 'paymentSaveButton' '保存' 90 70 90 28)

[void]$form.ShowDialog()
